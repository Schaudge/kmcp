// Copyright © 2020-2021 Wei Shen <shenwei356@gmail.com>
//
// Permission is hereby granted, free of charge, to any person obtaining a copy
// of this software and associated documentation files (the "Software"), to deal
// in the Software without restriction, including without limitation the rights
// to use, copy, modify, merge, publish, distribute, sublicense, and/or sell
// copies of the Software, and to permit persons to whom the Software is
// furnished to do so, subject to the following conditions:
//
// The above copyright notice and this permission notice shall be included in
// all copies or substantial portions of the Software.
//
// THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR
// IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF MERCHANTABILITY,
// FITNESS FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT. IN NO EVENT SHALL THE
// AUTHORS OR COPYRIGHT HOLDERS BE LIABLE FOR ANY CLAIM, DAMAGES OR OTHER
// LIABILITY, WHETHER IN AN ACTION OF CONTRACT, TORT OR OTHERWISE, ARISING FROM,
// OUT OF OR IN CONNECTION WITH THE SOFTWARE OR THE USE OR OTHER DEALINGS IN
// THE SOFTWARE.

package cmd

import (
	"fmt"
	"io"
	"io/ioutil"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/pkg/errors"
	"github.com/shenwei356/bio/seq"
	"github.com/shenwei356/bio/seqio/fastx"
	"github.com/shenwei356/util/cliutil"
	"github.com/shenwei356/util/pathutil"
	"github.com/spf13/cobra"
	"github.com/twotwotwo/sorts/sortutil"
)

var searchCmd = &cobra.Command{
	Use:   "search",
	Short: "Search sequence against a database",
	Long: `Search sequence against a database

Attentions:
  1. Input format should be (gzipped) FASTA or FASTQ from files or stdin.
  2. A long query sequences may contain duplicated k-mers, which are
     not removed for short sequences by default. You may modify the
     value of -u/--kmer-dedup-threshold to remove duplicates.
  3. For long reads or contigs, you should split them in to short reads
     using "seqkit sliding", e.g.,
         seqkit sliding -s 100 -W 300

Shared flags between "search" and "profile":
  1. -t/--min-query-cov.
  3. -N/--name-map.

Special attentions:
  1. The values of tCov and jacc in result only apply for single size of k-mer.

Performance tips:
  1. Increase value of -j/--threads for acceleratation, but values larger
     than number of CPU cores won't bring extra speedup.
  2. Use --low-mem for database larger than RAM, but the searching would be
     very very slow for a large number of queries.

`,
	Run: func(cmd *cobra.Command, args []string) {
		opt := getOptions(cmd)
		seq.ValidateSeq = false

		var fhLog *os.File
		if opt.Log2File {
			fhLog = addLog(opt.LogFile, opt.Verbose)
		}

		outputLog := opt.Verbose || opt.Log2File
		verbose := opt.Verbose

		timeStart := time.Now()
		defer func() {
			if outputLog {
				log.Info()
				log.Infof("elapsed time: %s", time.Since(timeStart))
				log.Info()
			}
			if opt.Log2File {
				fhLog.Close()
			}
		}()

		var err error

		// ---------------------------------------------------------------

		dbDir := getFlagString(cmd, "db-dir")
		if dbDir == "" {
			checkError(fmt.Errorf("flag -d/--db-dir needed"))
		}
		outFile := getFlagString(cmd, "out-file")
		minLen := getFlagNonNegativeInt(cmd, "min-query-len")
		queryCov := getFlagFloat64(cmd, "min-query-cov")
		targetCov := getFlagFloat64(cmd, "min-target-cov")
		minCount := getFlagPositiveInt(cmd, "min-kmers")
		useMmap := !getFlagBool(cmd, "low-mem")
		nameMappingFiles := getFlagStringSlice(cmd, "name-map")
		loadDefaultNameMap := getFlagBool(cmd, "default-name-map")
		keepUnmatched := getFlagBool(cmd, "keep-unmatched")
		// topN := getFlagNonNegativeInt(cmd, "keep-top")
		topN := 0
		topNScore := getFlagNonNegativeInt(cmd, "keep-top-scores")
		noHeaderRow := getFlagBool(cmd, "no-header-row")
		sortBy := getFlagString(cmd, "sort-by")
		doNotSort := getFlagBool(cmd, "do-not-sort")
		// keepOrder := getFlagBool(cmd, "keep-order")
		keepOrder := true
		wholeFile := getFlagBool(cmd, "query-whole-file")
		useFileName := getFlagBool(cmd, "use-filename")
		queryID := getFlagString(cmd, "query-id")
		deduplicateThreshold := getFlagPositiveInt(cmd, "kmer-dedup-threshold")
		// immediateOutput := getFlagBool(cmd, "immediate-output")

		if doNotSort && topNScore > 0 {
			log.Warningf("flag -n/--keep-top-scores ignored when -S/--do-not-sort given")
		}

		switch sortBy {
		case "qcov", "jacc", "tcov":
			break
		default:
			checkError(fmt.Errorf("invalid value for flag -s/--sort-by: %s. Available: qcov/tsov/jacc", sortBy))
		}

		if queryCov < 0 || queryCov > 1 {
			checkError(fmt.Errorf("value of -t/--query-cov should be in range [0, 1]"))
		}
		if targetCov < 0 || targetCov > 1 {
			checkError(fmt.Errorf("value of -T/-target-cov should be in range [0, 1]"))
		}

		// ---------------------------------------------------------------
		// check Database

		subFiles, err := ioutil.ReadDir(dbDir)
		if err != nil {
			checkError(fmt.Errorf("read database error: %s", err))
		}

		dbDirs := make([]string, 0, 8)
		for _, file := range subFiles {
			if file.Name() == "." || file.Name() == ".." {
				continue
			}
			path := filepath.Join(dbDir, file.Name())

			if !file.IsDir() {
				continue
			}
			existed, err := pathutil.Exists(filepath.Join(path, dbInfoFile))
			if err != nil {
				checkError(fmt.Errorf("read database error: %s", err))
			}
			if existed {
				dbDirs = append(dbDirs, path)
			}
		}
		if len(dbDirs) == 0 {
			checkError(fmt.Errorf("invalid kmcp database: %s", dbDir))
		}

		if outputLog {
			log.Infof("kmcp v%s", VERSION)
			log.Info("  https://github.com/shenwei356/kmcp")
			log.Info()
		}

		// ---------------------------------------------------------------
		// name mapping files

		var namesMap map[string]string
		mappingNames := len(nameMappingFiles) != 0
		if mappingNames {
			if outputLog {
				log.Infof("loading name mapping file ...")
			}
			nameMappingFile := nameMappingFiles[0]
			namesMap, err = cliutil.ReadKVs(nameMappingFile, false)
			if err != nil {
				checkError(errors.Wrap(err, nameMappingFile))
			}

			if len(nameMappingFiles) > 1 {
				for _, _nameMappingFile := range nameMappingFiles[1:] {
					_namesMap, err := cliutil.ReadKVs(_nameMappingFile, false)
					if err != nil {
						checkError(errors.Wrap(err, nameMappingFile))
					}
					for _k, _v := range _namesMap {
						namesMap[_k] = _v
					}
				}
			}

			if outputLog {
				log.Infof("  %d pairs of name mapping values from %d file(s) loaded", len(namesMap), len(nameMappingFiles))
			}

			// mappingNames = len(namesMap) > 0
		}

		// ---------------------------------------------------------------
		// input files

		if outputLog {
			log.Info("checking input files ...")
		}
		files := getFileListFromArgsAndFile(cmd, args, true, "infile-list", true)
		if outputLog {
			if len(files) == 1 && isStdin(files[0]) {
				log.Info("  no files given, reading from stdin")
			} else {
				log.Infof("  %d input file(s) given", len(files))
			}
		}

		outFileClean := filepath.Clean(outFile)
		for _, file := range files {
			if !isStdin(file) && filepath.Clean(file) == outFileClean {
				checkError(fmt.Errorf("out file should not be one of the input file"))
			}
		}

		// ---------------------------------------------------------------
		// load db

		if outputLog {
			if useMmap {
				log.Info("loading database with mmap enabled ...")
			} else {
				log.Info("loading database ...")
			}
		}
		searchOpt := SearchOptions{
			UseMMap: useMmap,
			Threads: opt.NumCPUs,
			Verbose: opt.Verbose,

			DeduplicateThreshold: deduplicateThreshold,

			TopN:       topN,
			TopNScores: topNScore,
			SortBy:     sortBy,
			DoNotSort:  doNotSort,

			MinQLen:      minLen,
			MinMatched:   minCount,
			MinQueryCov:  queryCov,
			MinTargetCov: targetCov,

			LoadDefaultNameMap: loadDefaultNameMap,
			NameMap:            namesMap,
		}
		sg, err := NewUnikIndexDBSearchEngine(searchOpt, dbDirs...)
		if err != nil {
			checkError(err)
		}

		for _, db := range sg.DBs {
			if queryCov <= db.Info.FPR {
				checkError(fmt.Errorf("query coverage threshold (%f) should not be smaller than FPR of single bloom filter of index database (%f)", queryCov, db.Info.FPR))
			}
		}
		if outputLog {
			log.Infof("database loaded: %s", dbDir)
			log.Info()
			log.Infof("-------------------- [main parameters] --------------------")
			log.Infof("  minimum    query length: %d", minLen)
			log.Infof("  minimum  matched k-mers: %d", minCount)
			log.Infof("  minimum  query coverage: %f", queryCov)
			log.Infof("  minimum target coverage: %f", targetCov)
			log.Infof("  minimum target coverage: %f", targetCov)
			log.Infof("-------------------- [main parameters] --------------------")
			log.Info()
			log.Info("searching ...")
		}

		timeStart1 := time.Now()

		outfh, gw, w, err := outStream(outFile, strings.HasSuffix(outFile, ".gz"), opt.CompressionLevel)
		checkError(err)
		defer func() {
			outfh.Flush()
			if gw != nil {
				gw.Close()
			}
			w.Close()
		}()

		if !noHeaderRow {
			outfh.WriteString("#query\tqLen\tqKmers\tFPR\thits\ttarget\tfragIdx\tfrags\ttLen\tkSize\tmKmers\tqCov\ttCov\tjacc\tqueryIdx\n")
		}

		var fastxReader *fastx.Reader
		var record *fastx.Record

		// ---------------------------------------------------------------
		// receive result and output

		var total, matched uint64
		var speed float64 // k reads/second

		donePrint := make(chan int)
		ch := make(chan *QueryResult, 1024)
		go func() {
			var query []byte
			var qLen, qKmers, FPR, hits string
			var target, fragIdx, frags, tLen, kSize, mKmers, qCov, tCov, jacc, queryIdx string

			for result := range ch {
				if result.Matches == nil {
					if !keepUnmatched {
						poolQueryResult.Put(result)
						continue
					}

					query = result.QueryID
					qLen = strconv.Itoa(result.QueryLen)
					qKmers = strconv.Itoa(result.NumKmers)
					FPR = strconv.FormatFloat(result.FPR, 'e', 4, 64)
					hits = "0"

					kSize = strconv.Itoa(result.K)
					queryIdx = strconv.Itoa(int(result.QueryIdx))

					target = ""
					fragIdx = "-1"
					frags = "0"
					tLen = "0"
					mKmers = "0"
					qCov = "0"
					tCov = "0"
					jacc = "0"

					outfh.Write(query)
					outfh.WriteByte('\t')
					outfh.WriteString(qLen)
					outfh.WriteByte('\t')
					outfh.WriteString(qKmers)
					outfh.WriteByte('\t')
					outfh.WriteString(FPR)
					outfh.WriteByte('\t')
					outfh.WriteString(hits)
					outfh.WriteByte('\t')

					outfh.WriteString(target)
					outfh.WriteByte('\t')
					outfh.WriteString(fragIdx)
					outfh.WriteByte('\t')
					outfh.WriteString(frags)
					outfh.WriteByte('\t')
					outfh.WriteString(tLen)
					outfh.WriteByte('\t')
					outfh.WriteString(kSize)
					outfh.WriteByte('\t')

					outfh.WriteString(mKmers)
					outfh.WriteByte('\t')
					outfh.WriteString(qCov)
					outfh.WriteByte('\t')
					outfh.WriteString(tCov)
					outfh.WriteByte('\t')
					outfh.WriteString(jacc)
					outfh.WriteByte('\t')
					outfh.WriteString(queryIdx)

					outfh.WriteByte('\n')

					poolQueryResult.Put(result)
					continue
				}

				// found
				matched++

				query = result.QueryID
				qLen = strconv.Itoa(result.QueryLen)
				qKmers = strconv.Itoa(result.NumKmers)
				FPR = strconv.FormatFloat(result.FPR, 'e', 4, 64)
				hits = strconv.Itoa(len(*result.Matches))

				kSize = strconv.Itoa(result.K)
				queryIdx = strconv.Itoa(int(result.QueryIdx))

				for _, match := range *result.Matches {

					target = match.Target[0]
					fragIdx = strconv.Itoa(int(uint16(match.TargetIdx[0])))
					frags = strconv.Itoa(int(match.TargetIdx[0] >> 16))
					tLen = strconv.Itoa(int(match.GenomeSize[0]))
					mKmers = strconv.Itoa(match.NumKmers)
					qCov = strconv.FormatFloat(match.QCov, 'f', 4, 64)
					tCov = strconv.FormatFloat(match.TCov, 'f', 4, 64)
					jacc = strconv.FormatFloat(match.JaccardIndex, 'f', 4, 64)

					outfh.Write(query)
					outfh.WriteByte('\t')
					outfh.WriteString(qLen)
					outfh.WriteByte('\t')
					outfh.WriteString(qKmers)
					outfh.WriteByte('\t')
					outfh.WriteString(FPR)
					outfh.WriteByte('\t')
					outfh.WriteString(hits)
					outfh.WriteByte('\t')

					outfh.WriteString(target)
					outfh.WriteByte('\t')
					outfh.WriteString(fragIdx)
					outfh.WriteByte('\t')
					outfh.WriteString(frags)
					outfh.WriteByte('\t')
					outfh.WriteString(tLen)
					outfh.WriteByte('\t')
					outfh.WriteString(kSize)
					outfh.WriteByte('\t')

					outfh.WriteString(mKmers)
					outfh.WriteByte('\t')
					outfh.WriteString(qCov)
					outfh.WriteByte('\t')
					outfh.WriteString(tCov)
					outfh.WriteByte('\t')
					outfh.WriteString(jacc)
					outfh.WriteByte('\t')
					outfh.WriteString(queryIdx)

					outfh.WriteByte('\n')
				}

				//if immediateOutput {
				// outfh.Flush()
				//}

				(*result.Matches) = (*(result.Matches))[:0]
				poolMatches.Put(result.Matches)

				poolQueryResult.Put(result)
			}
			donePrint <- 1
		}()

		done := make(chan int)
		go func() {
			if !keepOrder {
				var query []byte
				var qLen, qKmers, FPR, hits string
				var target, fragIdx, frags, tLen, kSize, mKmers, qCov, tCov, jacc, queryIdx string
				for result := range sg.OutCh {
					total++

					// output(result)
					if result.Matches == nil {
						if !keepUnmatched {
							poolQueryResult.Put(result)
							continue
						}

						query = result.QueryID
						qLen = strconv.Itoa(result.QueryLen)
						qKmers = strconv.Itoa(result.NumKmers)
						FPR = strconv.FormatFloat(result.FPR, 'e', 4, 64)
						hits = "0"

						kSize = strconv.Itoa(result.K)
						queryIdx = strconv.Itoa(int(result.QueryIdx))

						target = ""
						fragIdx = "-1"
						frags = "0"
						tLen = "0"
						mKmers = "0"
						qCov = "0"
						tCov = "0"
						jacc = "0"

						outfh.Write(query)
						outfh.WriteByte('\t')
						outfh.WriteString(qLen)
						outfh.WriteByte('\t')
						outfh.WriteString(qKmers)
						outfh.WriteByte('\t')
						outfh.WriteString(FPR)
						outfh.WriteByte('\t')
						outfh.WriteString(hits)
						outfh.WriteByte('\t')

						outfh.WriteString(target)
						outfh.WriteByte('\t')
						outfh.WriteString(fragIdx)
						outfh.WriteByte('\t')
						outfh.WriteString(frags)
						outfh.WriteByte('\t')
						outfh.WriteString(tLen)
						outfh.WriteByte('\t')
						outfh.WriteString(kSize)
						outfh.WriteByte('\t')

						outfh.WriteString(mKmers)
						outfh.WriteByte('\t')
						outfh.WriteString(qCov)
						outfh.WriteByte('\t')
						outfh.WriteString(tCov)
						outfh.WriteByte('\t')
						outfh.WriteString(jacc)
						outfh.WriteByte('\t')
						outfh.WriteString(queryIdx)

						outfh.WriteByte('\n')

						poolQueryResult.Put(result)
						continue
					}

					// found
					matched++

					query = result.QueryID
					qLen = strconv.Itoa(result.QueryLen)
					qKmers = strconv.Itoa(result.NumKmers)
					FPR = strconv.FormatFloat(result.FPR, 'e', 4, 64)
					hits = strconv.Itoa(len(*result.Matches))

					kSize = strconv.Itoa(result.K)
					queryIdx = strconv.Itoa(int(result.QueryIdx))

					for _, match := range *result.Matches {

						target = match.Target[0]
						fragIdx = strconv.Itoa(int(uint16(match.TargetIdx[0])))
						frags = strconv.Itoa(int(match.TargetIdx[0] >> 16))
						tLen = strconv.Itoa(int(match.GenomeSize[0]))
						mKmers = strconv.Itoa(match.NumKmers)
						qCov = strconv.FormatFloat(match.QCov, 'f', 4, 64)
						tCov = strconv.FormatFloat(match.TCov, 'f', 4, 64)
						jacc = strconv.FormatFloat(match.JaccardIndex, 'f', 4, 64)

						outfh.Write(query)
						outfh.WriteByte('\t')
						outfh.WriteString(qLen)
						outfh.WriteByte('\t')
						outfh.WriteString(qKmers)
						outfh.WriteByte('\t')
						outfh.WriteString(FPR)
						outfh.WriteByte('\t')
						outfh.WriteString(hits)
						outfh.WriteByte('\t')

						outfh.WriteString(target)
						outfh.WriteByte('\t')
						outfh.WriteString(fragIdx)
						outfh.WriteByte('\t')
						outfh.WriteString(frags)
						outfh.WriteByte('\t')
						outfh.WriteString(tLen)
						outfh.WriteByte('\t')
						outfh.WriteString(kSize)
						outfh.WriteByte('\t')

						outfh.WriteString(mKmers)
						outfh.WriteByte('\t')
						outfh.WriteString(qCov)
						outfh.WriteByte('\t')
						outfh.WriteString(tCov)
						outfh.WriteByte('\t')
						outfh.WriteString(jacc)
						outfh.WriteByte('\t')
						outfh.WriteString(queryIdx)

						outfh.WriteByte('\n')
					}

					//if immediateOutput {
					// outfh.Flush()
					//}

					(*result.Matches) = (*(result.Matches))[:0]
					poolMatches.Put(result.Matches)

					poolQueryResult.Put(result)
				}
			} else {
				m := make(map[uint64]*QueryResult, opt.NumCPUs)
				var id, _id uint64
				var ok bool

				for result := range sg.OutCh {
					if verbose {
						total++
						if (total < 8192 && total&63 == 0) || total&8191 == 0 {
							speed = float64(total) / 1000000 / time.Since(timeStart1).Minutes()
							fmt.Fprintf(os.Stderr, "processed queries: %d, speed: %.3f million queries per minute\r", total, speed)
						}
					}

					_id = result.QueryIdx

					if _id == id {
						// output(result)
						ch <- result

						id++
						continue
					}

					m[_id] = result

					if result, ok = m[id]; ok {
						// output(_result)
						ch <- result

						delete(m, id)
						id++
					}
				}

				if len(m) > 0 {
					ids := make([]uint64, len(m))
					i := 0
					for id = range m {
						ids[i] = id
						i++
					}
					sortutil.Uint64s(ids)
					for _, id = range ids {
						// output(m[id])
						ch <- m[id]
					}
				}
			}

			close(ch)
			done <- 1
		}()

		// ---------------------------------------------------------------
		// send query

		var id uint64
		for _, file := range files {
			if outputLog {
				log.Infof("reading sequence file: %s", file)
			}
			fastxReader, err = fastx.NewDefaultReader(file)
			checkError(errors.Wrap(err, file))

			if wholeFile {
				var recordID []byte
				var sequence *seq.Seq
				first := true
				for {
					record, err = fastxReader.Read()
					if err != nil {
						if err == io.EOF {
							break
						}
						errors.Wrap(err, file)
						break
					}

					if first {
						if useFileName {
							filename, _ := filepathTrimExtension(file)
							recordID = []byte(filename)
						} else if queryID != "" {
							recordID = []byte(queryID)
						} else {
							recordID = make([]byte, len(record.ID))
							copy(recordID, record.ID)
						}
						sequence = record.Seq.Clone2()
						first = false
					} else {
						sequence.Seq = append(sequence.Seq, record.Seq.Seq...)
					}
				}

				query := poolQuery.Get().(*Query)
				query.Idx = id
				query.ID = recordID
				query.Seq = sequence
				sg.InCh <- query

				// sg.InCh <- &Query{
				// 	Idx: id,
				// 	ID:  recordID,
				// 	Seq: sequence,
				// }

				id++

				continue
			}

			for {
				record, err = fastxReader.Read()
				if err != nil {
					if err == io.EOF {
						break
					}
					errors.Wrap(err, file)
					break
				}

				recordID := make([]byte, len(record.ID))
				copy(recordID, record.ID)

				query := poolQuery.Get().(*Query)
				query.Idx = id
				query.ID = recordID
				// query.Seq = record.Seq.Clone2()
				query.Seq = cloneFastx(record.Seq)
				sg.InCh <- query

				// sg.InCh <- &Query{
				// 	Idx: id,
				// 	ID:  recordID,
				// 	Seq: record.Seq.Clone2(),
				// }

				id++
			}
		}
		close(sg.InCh) // close Inch

		sg.Wait() // wait all searching finished
		<-done    // all result returned and outputed
		<-donePrint

		if outputLog {
			fmt.Fprintf(os.Stderr, "\n")

			speed = float64(total) / 1000000 / time.Since(timeStart1).Minutes()
			log.Infof("")
			log.Infof("processed queries: %d, speed: %.3f million queries per minute\n", total, speed)
			log.Infof("%.4f%% (%d/%d) queries matched", float64(matched)/float64(total)*100, matched, total)
			log.Infof("done searching")
		}

		checkError(sg.Close()) // cleanup
	},
}

func init() {
	RootCmd.AddCommand(searchCmd)

	// database option
	searchCmd.Flags().StringP("db-dir", "d", "", `database directory created by "kmcp index"`)
	searchCmd.Flags().BoolP("low-mem", "", false, `do not load all index files into memory, the searching would be very very slow for a large number of queries`)

	// query option
	searchCmd.Flags().IntP("kmer-dedup-threshold", "u", 256, `remove duplicated kmers for a query with >= N k-mers`)
	searchCmd.Flags().BoolP("query-whole-file", "g", false, `use the whole file as a query, e.g., for genome similarity estimation against k-mer sketch database`)
	searchCmd.Flags().BoolP("use-filename", "G", false, `use file name as query ID when using the whole file as a query`)
	searchCmd.Flags().StringP("query-id", "", "", `custom query Id when using the whole file as a query`)

	searchCmd.Flags().IntP("min-kmers", "c", 30, `minimal number of matched k-mers (sketches)`)
	searchCmd.Flags().IntP("min-query-len", "m", 70, `minimal query length`)
	searchCmd.Flags().Float64P("min-query-cov", "t", 0.55, `minimal query coverage, i.e., proportion of matched k-mers and unique k-mers of a query`)
	searchCmd.Flags().Float64P("min-target-cov", "T", 0, `minimal target coverage, i.e., proportion of matched k-mers and unique k-mers of a target`)

	// output
	searchCmd.Flags().StringP("out-file", "o", "-", `out file, supports and recommends a ".gz" suffix ("-" for stdout)`)
	searchCmd.Flags().StringSliceP("name-map", "N", []string{}, `tabular two-column file(s) mapping names to user-defined values`)
	searchCmd.Flags().BoolP("default-name-map", "D", false, `load ${db}/__name_mapping.tsv for mapping name first`)
	searchCmd.Flags().BoolP("keep-unmatched", "K", false, `keep unmatched query sequence information`)
	// searchCmd.Flags().BoolP("keep-order", "k", false, `keep results in order of input sequences`)
	// searchCmd.Flags().IntP("keep-top", "n", 0, `keep top N hits, 0 for all`)
	searchCmd.Flags().IntP("keep-top-scores", "n", 0, `keep matches with the top N score for a query, 0 for all`)
	searchCmd.Flags().BoolP("no-header-row", "H", false, `do not print header row`)
	searchCmd.Flags().StringP("sort-by", "s", "qcov", `sort hits by "qcov" (Containment Index), "tcov" or "jacc" (Jaccard Index)`)
	searchCmd.Flags().BoolP("do-not-sort", "S", false, `do not sort matches of a query`)
	// searchCmd.Flags().BoolP("immediate-output", "I", false, "print output immediately, do not use write buffer")

}

func cloneFastx(sequence *seq.Seq) *seq.Seq {
	s := make([]byte, len(sequence.Seq))
	copy(s, sequence.Seq)

	q := make([]byte, len(sequence.Qual))
	copy(q, sequence.Qual)

	qv := make([]int, len(sequence.QualValue))
	copy(qv, sequence.QualValue)

	// return &seq.Seq{
	// 	Alphabet:  sequence.Alphabet,
	// 	Seq:       s,
	// 	Qual:      q,
	// 	QualValue: qv,
	// }

	clone := poolSeq.Get().(*seq.Seq)
	clone.Alphabet = sequence.Alphabet
	clone.Seq = s
	clone.Qual = q
	clone.QualValue = qv
	return clone
}

var poolSeq = &sync.Pool{New: func() interface{} {
	return &seq.Seq{}
}}
