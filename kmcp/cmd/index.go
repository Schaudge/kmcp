// Copyright © 2020 Wei Shen <shenwei356@gmail.com>
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
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"sync"
	"time"

	"github.com/cespare/xxhash"
	"github.com/pkg/errors"
	"github.com/shenwei356/kmcp/kmcp/cmd/index"
	"github.com/shenwei356/unikmer"
	"github.com/shenwei356/util/bytesize"
	"github.com/shenwei356/util/pathutil"
	"github.com/spf13/cobra"
	"github.com/twotwotwo/sorts"
	"github.com/twotwotwo/sorts/sortutil"
	"github.com/vbauerster/mpb/v5"
	"github.com/vbauerster/mpb/v5/decor"
	"gopkg.in/yaml.v2"
)

var indexCmd = &cobra.Command{
	Use:   "index",
	Short: "Construct database from k-mer files",
	Long: `Construct database from k-mer files

We build index for k-mers (sketches) with modified compact bit-sliced
signature index (COBS) or repeated and merged bloom filter (RAMBO).

We totally rewrite the algorithms and try to combine their advantage.
	
Attentions:
  1. All input .unik files should be generated by "kmcp compute".

Tips:
  1. Increase value of -j/--threads for acceleratation in cost of more
     memory occupation and I/O pressure.
     #threads * #threads files are simultaneously opened, and max number
     of opened files is limited by flag -F/--max-open-files.
  2. Value of block size -b/--block-size better be multiple of 64.
  3. Use flag -m/--block-max-kmers-t1 and -M/--block-max-kmers-t2 to
     individually create index for input with very large number of k-mers,
     for precise control of index file size.
  4. Use --dry-run to adjust parameters and check final number of 
     index files (#index-files) and total file size. 
     #index-files >= #cpus is recommended for better parallelization.

Repeated and merged bloom filter (RAMBO)
  1. It's optional with flags -R/--num-repititions and -B/--num-buckets.
     Values of these flags should be carefully chosen after testing.
  2. For less than tens of thousands input files, these's no need to
	 use RAMBO index, which has much bigger database files and
     slightly higher false positive rate.

References:
  1. COBS: https://arxiv.org/abs/1905.09624
  2. RAMBO: https://arxiv.org/abs/1910.02611

`,
	Run: func(cmd *cobra.Command, args []string) {
		opt := getOptions(cmd)
		runtime.GOMAXPROCS(opt.NumCPUs)

		timeStart := time.Now()
		defer func() {
			if opt.Verbose {
				log.Info()
				log.Infof("elapsed time: %s", time.Since(timeStart))
			}
		}()

		// ---------------------------------------------------------------
		// basic flags

		var err error

		dryRun := getFlagBool(cmd, "dry-run")
		if dryRun {
			opt.Verbose = true
		}

		outDir := getFlagString(cmd, "out-dir")

		inDir := getFlagString(cmd, "in-dir")

		readFromDir := inDir != ""
		if readFromDir {
			var isDir bool
			isDir, err = pathutil.IsDir(inDir)
			if err != nil {
				checkError(errors.Wrapf(err, "checking -I/--in-dir"))
			}
			if !isDir {
				checkError(fmt.Errorf("value of -I/--in-dir should be a directory: %s", inDir))
			}
		}

		reFileStr := getFlagString(cmd, "file-regexp")
		var reFile *regexp.Regexp
		if reFileStr != "" {
			if !reIgnoreCase.MatchString(reFileStr) {
				reFileStr = reIgnoreCaseStr + reFileStr
			}
			reFile, err = regexp.Compile(reFileStr)
			checkError(errors.Wrapf(err, "parsing regular expression for matching file: %s", reFileStr))
		}

		force := getFlagBool(cmd, "force")

		alias := getFlagString(cmd, "alias")

		// ---------------------------------------------------------------
		// index flags

		sBlock00 := getFlagInt(cmd, "block-size")

		fpr := getFlagPositiveFloat64(cmd, "false-positive-rate")
		numHashes := getFlagPositiveInt(cmd, "num-hash")
		if numHashes > 255 {
			checkError(fmt.Errorf("value of -n/--num-hash too big: %d", numHashes))
		}

		maxOpenFiles := getFlagPositiveInt(cmd, "max-open-files")

		// block-max-kmers-t1
		kmerThreshold8Str := getFlagString(cmd, "block-max-kmers-t1")
		kmerThreshold8Float, err := bytesize.ParseByteSize(kmerThreshold8Str)
		if err != nil {
			checkError(fmt.Errorf("invalid size: %s", kmerThreshold8Str))
		}
		if kmerThreshold8Float <= 0 {
			checkError(fmt.Errorf("value of flag -m/--block-max-kmers-t1 should be positive: %d", kmerThreshold8Float))
		}
		kmerThreshold8 := uint64(kmerThreshold8Float)

		// block-max-kmers-t2
		kmerThresholdSStr := getFlagString(cmd, "block-max-kmers-t2")
		kmerThresholdSFloat, err := bytesize.ParseByteSize(kmerThresholdSStr)
		if err != nil {
			checkError(fmt.Errorf("invalid size: %s", kmerThresholdSStr))
		}
		if kmerThresholdSFloat <= 0 {
			checkError(fmt.Errorf("value of flag -M/--block-max-kmers-t2 should be positive: %d", kmerThresholdSFloat))
		}
		kmerThresholdS := uint64(kmerThresholdSFloat)

		if kmerThreshold8 >= kmerThresholdS {
			checkError(fmt.Errorf("value of flag -m/--block-max-kmers-t1 (%d) should be small than -M/--block-max-kmers-t2 (%d)", kmerThreshold8, kmerThresholdS))
		}

		// ---------------------------------------------------------------
		// index flags
		numRepeats := getFlagPositiveInt(cmd, "num-repititions")
		numBuckets := getFlagNonNegativeInt(cmd, "num-buckets")
		if numRepeats < 1 {
			checkError(fmt.Errorf("value of -R/--num-repititions should be >= 1"))
		}
		if numBuckets > 0 && sBlock00 > numBuckets {
			checkError(fmt.Errorf("value of -b/--block-size (%d) should be small than -B/--num-buckets (%d)", sBlock00, numBuckets))
		}
		seed := getFlagPositiveInt(cmd, "seed")

		// ---------------------------------------------------------------
		// out dir

		if outDir == "" {
			if inDir != "" {
				outDir = filepath.Clean(inDir) + ".kmcp-db"
			} else {
				outDir = "kmcp-db"
			}
		}
		if !dryRun {
			makeOutDir(outDir, force)
		}
		if alias == "" {
			alias = filepath.Base(outDir)
		}

		// ---------------------------------------------------------------
		// cached file info

		fileInfoCache := filepath.Join(inDir, fileUnikInfos)

		var hasInfoCache bool
		var InfoCacheOK bool

		var k int = -1
		var hashed bool
		var canonical bool
		var scaled bool
		var scale uint32
		var meta0 Meta

		var files []string
		var nfiles int
		var n uint64
		var namesMap0 map[string]interface{}

		var reader0 *unikmer.Reader

		getInfo := func(file string, first bool) UnikFileInfo {
			infh, r, _, err := inStream(file)
			checkError(err)

			reader, err := unikmer.NewReader(infh)
			checkError(errors.Wrap(err, file))

			var meta Meta

			if len(reader.Description) > 0 {
				err := json.Unmarshal(reader.Description, &meta)
				if err != nil {
					checkError(fmt.Errorf("unsupported metadata: %s", reader.Description))
				}
			}

			if first {
				reader0 = reader
				k = reader.K
				hashed = reader.IsHashed()
				if !hashed {
					checkError(fmt.Errorf(`flag 'hashed' is supposed to be true, are the files created by 'kmcp compute'? %s`, file))
				}

				canonical = reader.IsCanonical()
				if !canonical {
					checkError(fmt.Errorf(`files with 'canonical' flag needed: %s`, file))
				}
				scaled = reader.IsScaled()
				scale = reader.GetScale()

				meta0 = meta
			} else {
				checkCompatibility(reader0, reader, file, &meta0, &meta)
				if scaled && scale != reader.GetScale() {
					checkError(fmt.Errorf(`scales not consistent, please check with "unikmer info": %s`, file))
				}
			}

			if reader.Number < 0 {
				checkError(fmt.Errorf("binary file not sorted or no k-mers number found: %s", file))
			}

			checkError(r.Close())
			return UnikFileInfo{Path: file, Name: meta.SeqID, Index: meta.FragIdx, Kmers: reader.Number}
		}

		fileInfos0 := make([]UnikFileInfo, 0, 1024)

		hasInfoCache, err = pathutil.Exists(fileInfoCache)
		if err != nil {
			checkError(fmt.Errorf("check .unik file info file: %s", err))
		}
		if hasInfoCache {
			if opt.Verbose {
				log.Infof("loading .unik file infos from file: %s", fileInfoCache)
			}

			// read
			fileInfos0, err = readUnikFileInfos(fileInfoCache)
			if err != nil {
				checkError(fmt.Errorf("fail to read fileinfo cache file: %s", err))
			}

			nfiles = len(fileInfos0)
			if opt.Verbose {
				log.Infof("%d cached file infos loaded", nfiles)
			}

			if len(fileInfos0) == 0 {
				InfoCacheOK = false
			} else {
				namesMap0 = make(map[string]interface{}, 1024)

				for _, info := range fileInfos0 {
					n += info.Kmers
					namesMap0[info.Name] = struct{}{}
				}

				// read some basic data
				getInfo(fileInfos0[0].Path, true)

				InfoCacheOK = true
			}
		}

		if hasInfoCache && InfoCacheOK {
			// do not have to check file again
		} else {

			// ---------------------------------------------------------------
			// input files

			if opt.Verbose {
				log.Info("checking input files ...")
			}

			if readFromDir {
				files, err = getFileListFromDir(inDir, reFile, opt.NumCPUs)
				checkError(errors.Wrapf(err, "err on walking dir: %s", inDir))
				if len(files) == 0 {
					log.Warningf("no files matching patttern: %s", reFileStr)
				}
			} else {
				files = getFileListFromArgsAndFile(cmd, args, true, "infile-list", true)
				if opt.Verbose {
					if len(files) == 1 && isStdin(files[0]) {
						log.Info("no files given, reading from stdin")
					}
				}
			}
			if opt.Verbose {
				log.Infof("%d input file(s) given", len(files))
			}
			nfiles = len(files)

			// ---------------------------------------------------------------
			// check unik files and read k-mers numbers

			if opt.Verbose {
				log.Info("checking .unik files ...")
			}

			var pbs *mpb.Progress
			var bar *mpb.Bar
			var chDuration chan time.Duration
			var doneDuration chan int

			if opt.Verbose {
				pbs = mpb.New(mpb.WithWidth(40), mpb.WithOutput(os.Stderr))
				bar = pbs.AddBar(int64(len(files)),
					mpb.BarStyle("[=>-]<+"),
					mpb.PrependDecorators(
						decor.Name("checking .unik file: ", decor.WC{W: len("checking .unik file: "), C: decor.DidentRight}),
						decor.Name("", decor.WCSyncSpaceR),
						decor.CountersNoUnit("%d / %d", decor.WCSyncWidth),
					),
					mpb.AppendDecorators(
						decor.Name("ETA: ", decor.WC{W: len("ETA: ")}),
						decor.EwmaETA(decor.ET_STYLE_GO, 60),
						decor.OnComplete(decor.Name(""), ". done"),
					),
				)

				chDuration = make(chan time.Duration, opt.NumCPUs)
				doneDuration = make(chan int)
				go func() {
					for t := range chDuration {
						bar.Increment()
						bar.DecoratorEwmaUpdate(t)
					}
					doneDuration <- 1
				}()
			}

			// first file
			file := files[0]
			var t time.Time
			if opt.Verbose {
				t = time.Now()
			}

			info := getInfo(file, true)
			n += info.Kmers
			if opt.Verbose {
				bar.Increment()
				bar.DecoratorEwmaUpdate(time.Since(t))
			}

			fileInfos0 = append(fileInfos0, info)
			namesMap0 = make(map[string]interface{}, 1024)
			namesMap := make(map[uint64]interface{}, nfiles)
			namesMap[xxhash.Sum64String(fmt.Sprintf("%s%s%d", info.Name, sepNameIdx, info.Index))] = struct{}{}
			namesMap0[info.Name] = struct{}{}

			// left files
			var wgGetInfo sync.WaitGroup
			chInfos := make(chan UnikFileInfo, opt.NumCPUs)
			tokensGetInfo := make(chan int, opt.NumCPUs)
			doneGetInfo := make(chan int)
			go func() {
				var ok bool
				var nameHash uint64
				for info := range chInfos {
					fileInfos0 = append(fileInfos0, info)
					n += info.Kmers

					nameHash = xxhash.Sum64String(fmt.Sprintf("%s%s%d", info.Name, sepNameIdx, info.Index))
					if _, ok = namesMap[nameHash]; ok {
						log.Warningf("duplicated name: %s", info.Name)
					} else {
						namesMap[nameHash] = struct{}{}
						if _, ok = namesMap0[info.Name]; !ok {
							namesMap0[info.Name] = struct{}{}
						}
					}
				}
				doneGetInfo <- 1
			}()

			for _, file := range files[1:] {
				wgGetInfo.Add(1)
				tokensGetInfo <- 1
				go func(file string) {
					defer func() {
						wgGetInfo.Done()
						<-tokensGetInfo
					}()
					var t time.Time
					if opt.Verbose {
						t = time.Now()
					}

					chInfos <- getInfo(file, false)

					if opt.Verbose {
						chDuration <- time.Duration(float64(time.Since(t)) / float64(opt.NumCPUs))
					}
				}(file)
			}

			wgGetInfo.Wait()
			close(chInfos)
			<-doneGetInfo

			if opt.Verbose {
				close(chDuration)
				<-doneDuration
				pbs.Wait()
			}

			if opt.Verbose {
				log.Infof("finished checking %d .unik files", nfiles)
			}
		}

		// ------------------------------------------------------------------------------------
		// .unik info

		if !hasInfoCache || !InfoCacheOK { // dump to info file
			log.Infof("write unik file info to file: %s", fileInfoCache)
			dumpUnikFileInfos(fileInfos0, fileInfoCache)
		}

		// ------------------------------------------------------------------------------------
		// begin creating index
		if opt.Verbose {
			log.Info()
			log.Infof("-------------------- [main parameters] --------------------")
			log.Infof("number of hashes: %d", numHashes)
			log.Infof("false positive rate: %f", fpr)

			if meta0.Minimizer {
				log.Infof("minimizer window: %d", meta0.MinimizerW)
			}
			if meta0.Syncmer {
				log.Infof("bounded syncmer size: %d", meta0.SyncmerS)
			}
			if meta0.SplitSeq {
				log.Infof("split seqequence size: %d, overlap: %d", meta0.SplitSize, meta0.SplitOverlap)
			}
			if scaled {
				log.Infof("down-sampling scale: %d", scale)
			}
			log.Infof("block-max-kmers-threshold 1: %s", bytesize.ByteSize(kmerThreshold8))
			log.Infof("block-max-kmers-threshold 2: %s", bytesize.ByteSize(kmerThresholdS))
			log.Infof("-------------------- [main parameters] --------------------")
			log.Info()
			log.Infof("building index ...")
		}

		// ------------------------------------------------------------------------------------

		singleRepeat := numRepeats == 1
		var singleSet bool
		if numBuckets == 0 { // special case, just like BIGSI/cobs
			singleSet = true
			numBuckets = len(fileInfos0)
			numRepeats = 1
		}
		numBucketsUint64 := uint64(numBuckets)

		var fileSize0 float64

		var totalIndexFiles int

		var pbs *mpb.Progress

		// repeatedly randomly shuffle names into buckets
		for rr := 0; rr < numRepeats; rr++ {
			dirR := fmt.Sprintf("R%03d", rr+1)
			runtime.GC()

			buckets := make([][]UnikFileInfo, numBuckets)
			var bIdx int
			var h1, h2 uint32
			for jj, info := range fileInfos0 {
				if singleSet {
					bIdx = jj
				} else {
					h1, h2 = baseHashes(xxhash.Sum64([]byte(info.Path)))
					bIdx = int(uint64(h1+h2*uint32(rr+seed)) % numBucketsUint64) // add seed
				}

				if buckets[bIdx] == nil {
					buckets[bIdx] = make([]UnikFileInfo, 0, 8)
				}
				buckets[bIdx] = append(buckets[bIdx], info)
			}

			fileInfoGroups := make([]UnikFileInfoGroup, len(buckets))
			for bb, infos := range buckets {
				var totalKmers uint64
				for _, info := range infos {
					totalKmers += info.Kmers
				}
				fileInfoGroups[bb] = UnikFileInfoGroup{Infos: infos, Kmers: totalKmers}
			}

			// sort by group kmer size
			sorts.Quicksort(UnikFileInfoGroups(fileInfoGroups))

			nFiles := len(fileInfoGroups)
			var sBlock int
			if sBlock00 <= 0 { // block size from command line
				sBlock = (int(float64(nFiles)/float64(runtime.NumCPU())) + 7) / 8 * 8
			} else {
				sBlock = sBlock00
			}

			if sBlock < 8 {
				sBlock = 8
			} else if sBlock > nFiles {
				sBlock = nFiles
			}

			if opt.Verbose {
				log.Info()
				if singleRepeat {
					log.Infof("block size: %d", sBlock)
				} else {
					log.Infof("[Repeat %d/%d] block size: %d", rr+1, numRepeats, sBlock)
				}
			}

			if opt.Verbose {
				pbs = mpb.New(mpb.WithWidth(50), mpb.WithOutput(os.Stderr))
			}

			// really begin
			nIndexFiles := int((nFiles + sBlock - 1) / sBlock) // may be more if using -m and -M
			indexFiles := make([]string, 0, nIndexFiles)

			ch := make(chan string, nIndexFiles)
			done := make(chan int)
			go func() {
				for f := range ch {
					indexFiles = append(indexFiles, f)
				}
				done <- 1
			}()

			var fileSize float64
			chFileSize := make(chan float64, nIndexFiles)
			doneFileSize := make(chan int)
			go func() {
				for f := range chFileSize {
					fileSize += f
				}
				doneFileSize <- 1
			}()

			var prefix string

			var b int
			var wg0 sync.WaitGroup
			maxConc := opt.NumCPUs
			if dryRun {
				maxConc = 1 // just for loging in order
			}
			tokens0 := make(chan int, maxConc)
			tokensOpenFiles := make(chan int, maxOpenFiles)

			sBlock0 := sBlock // save for later use

			batch := make([][]UnikFileInfo, 0, sBlock)
			var flag8, flag bool
			var lastInfos []UnikFileInfo
			var infoGroup UnikFileInfoGroup

			for i := 0; i <= nFiles; i++ {
				if i == nFiles { // process lastInfo
					// add previous file to batch
					if flag || flag8 {
						if lastInfos != nil {
							batch = append(batch, lastInfos)
							lastInfos = nil
						}
					}
				} else {
					infoGroup = fileInfoGroups[i]
					infos := infoGroup.Infos
					if infoGroup.Kmers == 0 { // skip empty buckets
						continue
					}

					if flag || flag8 {
						// add previous file to batch
						if lastInfos != nil {
							batch = append(batch, lastInfos)
							lastInfos = nil
						}

						if flag {
							lastInfos = infos // leave this file process in the next round
							// and we have to process files aleady in batch
						} else if infoGroup.Kmers > kmerThresholdS {
							// meet a very big file the first time
							flag = true       // mark
							lastInfos = infos // leave this file process in the next round
							// and we have to process files aleady in batch
						} else { // flag8 && !flag
							batch = append(batch, infos)
							if len(batch) < sBlock { // not filled
								continue
							}
						}
					} else if infoGroup.Kmers > kmerThreshold8 {
						if infoGroup.Kmers > kmerThresholdS {
							// meet a very big file the first time
							flag = true       // mark
							lastInfos = infos // leave this file process in the next round
							// and we have to process files aleady in batch
						} else {
							// meet a big file > kmerThreshold8
							sBlock = 8
							flag8 = true      // mark
							lastInfos = infos // leave this file process in the next batch
							// and we have to process files aleady in batch
						}
					} else {
						batch = append(batch, infos)
						if len(batch) < sBlock { // not filled
							continue
						}
					}

				}

				if len(batch) == 0 {
					if lastInfos == nil {
						break
					} else {
						continue
					}
				}

				b++

				if singleRepeat {
					prefix = fmt.Sprintf("[block #%03d]", b)
				} else {
					prefix = fmt.Sprintf("[Repeat %d/%d][block #%03d]", rr+1, numRepeats, b)
				}

				wg0.Add(1)
				tokens0 <- 1

				var bar *mpb.Bar
				if opt.Verbose && !dryRun {
					bar = pbs.AddBar(int64((len(batch)+7)/8),
						mpb.PrependDecorators(
							decor.Name(prefix, decor.WC{W: len(prefix) + 1, C: decor.DidentRight}),
							decor.CountersNoUnit("%d / %d", decor.WCSyncWidth),
						),
						mpb.AppendDecorators(decor.Percentage(decor.WC{W: 5})),
						mpb.BarFillerClearOnComplete(),
					)
				}

				go func(batch [][]UnikFileInfo, b int, prefix string, bar *mpb.Bar) {
					var wg sync.WaitGroup
					tokens := make(chan int, opt.NumCPUs)

					// max elements of UNION of all sets,
					// but it takes time to compute for reading whole data,
					// so we use sum of elements, which is slightly higher than actual size.
					var maxElements uint64
					var totalKmer uint64
					for _, infos := range batch {
						totalKmer = 0
						for _, info := range infos {
							totalKmer += info.Kmers
						}
						if maxElements < totalKmer {
							maxElements = totalKmer
						}
					}

					nInfoGroups := len(batch)
					var nBatchFiles int
					nBatchFiles = int((nInfoGroups + 7) / 8)

					sigsBlock := make([][]byte, 0, nBatchFiles)

					namesBlock := make([][]string, 0, nInfoGroups)
					indicesBlock := make([][]uint32, 0, nInfoGroups)
					sizesBlock := make([]uint64, 0, nInfoGroups)

					chBatch8 := make(chan batch8s, nBatchFiles)
					doneBatch8 := make(chan int)

					buf := make(map[int]batch8s)

					go func() {
						var id int = 1
						for batch2 := range chBatch8 {
							if batch2.id == id {
								sigsBlock = append(sigsBlock, batch2.sigs)
								namesBlock = append(namesBlock, batch2.names...)
								indicesBlock = append(indicesBlock, batch2.indices...)
								sizesBlock = append(sizesBlock, batch2.sizes...)
								if opt.Verbose && !dryRun {
									bar.Increment()
								}
								id++
								continue
							}
							for {
								if _batch, ok := buf[id]; ok {
									sigsBlock = append(sigsBlock, _batch.sigs)
									namesBlock = append(namesBlock, _batch.names...)
									indicesBlock = append(indicesBlock, _batch.indices...)
									sizesBlock = append(sizesBlock, _batch.sizes...)
									if opt.Verbose && !dryRun {
										bar.Increment()
									}
									delete(buf, id)
									id++
								} else {
									break
								}
							}
							buf[batch2.id] = batch2
						}
						if len(buf) > 0 {
							ids := make([]int, 0, len(buf))
							for id := range buf {
								ids = append(ids, id)
							}
							sort.Ints(ids)
							for _, id := range ids {
								_batch := buf[id]

								sigsBlock = append(sigsBlock, _batch.sigs)
								namesBlock = append(namesBlock, _batch.names...)
								indicesBlock = append(indicesBlock, _batch.indices...)
								sizesBlock = append(sizesBlock, _batch.sizes...)
								if opt.Verbose && !dryRun {
									bar.Increment()
								}
							}
						}

						doneBatch8 <- 1
					}()

					numSigs := CalcSignatureSize(uint64(maxElements), numHashes, fpr)
					var eFileSize float64
					eFileSize = 24
					for _, infos := range batch {
						eFileSize += 8 // length of Names (4) and indices (4)
						for _, info := range infos {
							// name + "\n" (1) + indice (4) + size (8)
							eFileSize += float64(len(info.Name) + 13)
						}
					}
					eFileSize += float64(numSigs * uint64(nBatchFiles))

					if opt.Verbose && dryRun {
						if singleRepeat {
							log.Infof("%s #files: %d, max #k-mers: %d, #signatures: %d, file size: %8s",
								prefix, len(batch), maxElements, numSigs, bytesize.ByteSize(eFileSize))
						} else {
							log.Infof("%s #buckets: %d, max #k-mers: %d, #signatures: %d, file size: %8s",
								prefix, len(batch), maxElements, numSigs, bytesize.ByteSize(eFileSize))
						}
					}

					// split into batches with 8 files
					var bb, jj int
					for ii := 0; ii < nInfoGroups; ii += 8 {
						if dryRun {
							continue
						}
						jj = ii + 8
						if jj > nInfoGroups {
							jj = nInfoGroups
						}
						wg.Add(1)
						tokens <- 1
						bb++

						var outFile string

						// 8 files
						go func(_batch [][]UnikFileInfo, bb int, numSigs uint64, outFile string, id int) {
							defer func() {
								wg.Done()
								<-tokens
							}()

							names := make([][]string, 0, 8)
							indices := make([][]uint32, 0, 8)
							sizes := make([]uint64, 0, 8)
							for _, infos := range _batch {
								_names := make([]string, len(infos))
								_indices := make([]uint32, len(infos))
								var _size uint64

								sorts.Quicksort(UnikFileInfosByName(infos))

								for iii, info := range infos {
									_names[iii] = info.Name
									_indices[iii] = info.Index
									_size += info.Kmers
								}
								names = append(names, _names)
								indices = append(indices, _indices)
								sizes = append(sizes, uint64(_size))
							}

							sigs := make([]byte, numSigs)

							numSigsM1 := numSigs - 1

							// every file in 8 file groups
							for _k, infos := range _batch {
								for _, info := range infos {
									tokensOpenFiles <- 1

									var infh *bufio.Reader
									var r *os.File
									var reader *unikmer.Reader
									var err error
									var code uint64
									var loc int

									infh, r, _, err = inStream(info.Path)
									checkError(errors.Wrap(err, info.Path))

									reader, err = unikmer.NewReader(infh)
									checkError(errors.Wrap(err, info.Path))
									singleHash := numHashes == 1

									if reader.IsHashed() {
										if singleHash {
											for {
												code, _, err = reader.ReadCodeWithTaxid()
												if err != nil {
													if err == io.EOF {
														break
													}
													checkError(errors.Wrap(err, info.Path))
												}

												// sigs[code%numSigs] |= 1 << (7 - _k)
												sigs[code&numSigsM1] |= 1 << (7 - _k) // &Xis faster than %X when X is power of 2
											}
										} else {
											for {
												code, _, err = reader.ReadCodeWithTaxid()
												if err != nil {
													if err == io.EOF {
														break
													}
													checkError(errors.Wrap(err, info.Path))
												}

												// for _, loc = range hashLocations(code, numHashes, numSigs) {
												for _, loc = range hashLocationsFaster(code, numHashes, numSigsM1) {
													sigs[loc] |= 1 << (7 - _k)
												}
											}
										}
									} else {
										if singleHash {
											for {
												code, _, err = reader.ReadCodeWithTaxid()
												if err != nil {
													if err == io.EOF {
														break
													}
													checkError(errors.Wrap(err, info.Path))
												}

												// sigs[hash64(code)%numSigs] |= 1 << (7 - _k)
												sigs[hash64(code)&numSigsM1] |= 1 << (7 - _k) // &Xis faster than %X when X is power of 2
											}
										} else {
											for {
												code, _, err = reader.ReadCodeWithTaxid()
												if err != nil {
													if err == io.EOF {
														break
													}
													checkError(errors.Wrap(err, info.Path))
												}

												// for _, loc = range hashLocations(code, numHashes, numSigs) {
												for _, loc = range hashLocationsFaster(hash64(code), numHashes, numSigsM1) {
													sigs[loc] |= 1 << (7 - _k)
												}
											}
										}
									}

									r.Close()

									<-tokensOpenFiles
								}
							}

							chBatch8 <- batch8s{
								id:      id,
								sigs:    sigs,
								names:   names,
								indices: indices,
								sizes:   sizes,
							}
						}(batch[ii:jj], bb, numSigs, outFile, bb)
					}

					wg.Wait()
					close(chBatch8)
					<-doneBatch8

					blockFile := filepath.Join(outDir,
						dirR,
						fmt.Sprintf("_block%03d%s", b, extIndex))

					if !dryRun {

						outfh, gw, w, err := outStream(blockFile, false, opt.CompressionLevel)
						checkError(err)
						defer func() {
							outfh.Flush()
							if gw != nil {
								gw.Close()
							}
							w.Close()
						}()

						writer, err := index.NewWriter(outfh, k, canonical, uint8(numHashes), numSigs, namesBlock, indicesBlock, sizesBlock)
						checkError(err)
						defer func() {
							checkError(writer.Flush())
						}()

						if nBatchFiles == 1 {
							checkError(writer.WriteBatch(sigsBlock[0], len(sigsBlock[0])))
						} else {
							row := make([]byte, nBatchFiles)
							for ii := 0; ii < int(numSigs); ii++ {
								for jj = 0; jj < nBatchFiles; jj++ {
									row[jj] = sigsBlock[jj][ii]
								}
								checkError(writer.Write(row))
							}
						}
					}

					ch <- filepath.Base(blockFile)
					chFileSize <- eFileSize

					wg0.Done()
					<-tokens0
				}(batch, b, prefix, bar)

				batch = make([][]UnikFileInfo, 0, sBlock)
			}

			wg0.Wait()

			close(ch)
			close(chFileSize)
			<-done
			<-doneFileSize

			if opt.Verbose && !dryRun {
				pbs.Wait()
			}

			totalIndexFiles += len(indexFiles)

			sortutil.Strings(indexFiles)
			dbInfo := NewUnikIndexDBInfo(indexFiles)
			dbInfo.Alias = alias
			dbInfo.K = k
			dbInfo.Hashed = hashed
			dbInfo.Kmers = n
			dbInfo.FPR = fpr
			dbInfo.BlockSize = sBlock0
			dbInfo.NumNames = len(fileInfoGroups)
			dbInfo.NumHashes = numHashes
			dbInfo.Canonical = canonical
			dbInfo.Scaled = scaled
			dbInfo.Scale = scale
			dbInfo.Minimizer = meta0.Minimizer
			dbInfo.MinimizerW = uint32(meta0.MinimizerW)
			dbInfo.Syncmer = meta0.Syncmer
			dbInfo.SyncmerS = uint32(meta0.SyncmerS)
			dbInfo.SplitSeq = meta0.SplitSeq
			dbInfo.SplitSize = meta0.SplitSize
			dbInfo.SplitOverlap = meta0.SplitOverlap

			if !dryRun {
				var n2 int
				n2, err = dbInfo.WriteTo(filepath.Join(outDir, dirR, dbInfoFile))
				checkError(err)
				fileSize += float64(n2)

				// write name_mapping.tsv
				func() {
					outfh, gw, w, err := outStream(filepath.Join(outDir, dirR, dbNameMappingFile), false, opt.CompressionLevel)
					checkError(err)
					defer func() {
						outfh.Flush()
						if gw != nil {
							gw.Close()
						}
						w.Close()
					}()

					var line string
					for name := range namesMap0 {
						line = fmt.Sprintf("%s\t%s\n", name, name)
						fileSize += float64(len(line))
						outfh.WriteString(line)
					}
				}()
			} else { // compute file size of __db.yaml and __name_mapping.tsv
				// __db.yaml
				data, err := yaml.Marshal(dbInfo)
				if err != nil {
					checkError(fmt.Errorf("fail to marshal database info"))
				}
				fileSize += float64(len(data))

				// __name_mapping.tsv
				var line string
				for name := range namesMap0 {
					line = fmt.Sprintf("%s\t%s\n", name, name)
					fileSize += float64(len(line))
				}
			}

			// ------------------------------------------------------------------------------------
			fileSize0 += fileSize
		}

		if opt.Verbose {
			log.Info()
			log.Infof("kmcp database with %d k-mers saved to %s", n, outDir)
			log.Infof("total file size: %s", bytesize.ByteSize(fileSize0))
			log.Infof("total index files: %d", totalIndexFiles)
		}
	},
}

func init() {
	RootCmd.AddCommand(indexCmd)

	indexCmd.Flags().StringP("in-dir", "I", "", `directory containing .unik files. directory symlinks are followed`)
	indexCmd.Flags().StringP("file-regexp", "", ".unik$", `regular expression for matching files in -I/--in-dir to index, case ignored`)

	indexCmd.Flags().StringP("out-dir", "O", "", `output directory. default: ${indir}.kmcp-db`)
	indexCmd.Flags().StringP("alias", "a", "", `database alias/name, default: basename of --out-dir. you can also manually edit it in info file: ${outdir}/__db.yml`)

	indexCmd.Flags().Float64P("false-positive-rate", "f", 0.3, `false positive rate of single bloom filter`)
	indexCmd.Flags().IntP("num-hash", "n", 1, `number of hashes of bloom filters`)
	indexCmd.Flags().IntP("block-size", "b", 0, `block size, better be multiple of 64 for large number of input files. default: min(#.files/#cpu, 8)`)
	indexCmd.Flags().StringP("block-max-kmers-t1", "m", "20M", `if k-mers of single .unik file exceeds this threshold, block size is changed to 8. unit supported: K, M, G`)
	indexCmd.Flags().StringP("block-max-kmers-t2", "M", "200M", `if k-mers of single .unik file exceeds this threshold, an individual index is created for this file. unit supported: K, M, G`)

	indexCmd.Flags().IntP("num-repititions", "R", 1, `number of repititions`)
	indexCmd.Flags().IntP("num-buckets", "B", 0, `number of buckets per repitition, 0 for one set per bucket`)
	indexCmd.Flags().IntP("seed", "", 1, `seed for randomly assigning names to buckets`)

	indexCmd.Flags().BoolP("force", "", false, `overwrite output directory`)
	indexCmd.Flags().IntP("max-open-files", "F", 256, `maximum number of opened files`)
	indexCmd.Flags().BoolP("dry-run", "", false, `dry run, useful for adjusting parameters (recommended)`)
}

// batch8 contains data from 8 files, just for keeping order of all files of a block
type batch8s struct {
	id int

	sigs    []byte
	names   [][]string
	indices [][]uint32
	sizes   []uint64
}

var sepNameIdx = "-id"

// compute exact max elements by reading all file. not used.
func maxElements(opt Options, tokensOpenFiles chan int, batch [][]UnikFileInfo) (maxElements int64) {
	var _wg sync.WaitGroup
	_tokens := make(chan int, opt.NumCPUs)
	_ch := make(chan int64, len(batch))
	_done := make(chan int)
	go func() {
		var totalKmer int64
		for totalKmer = range _ch {
			if maxElements < totalKmer {
				maxElements = totalKmer
			}
		}
		_done <- 1
	}()
	for _, infos := range batch {
		_wg.Add(1)
		_tokens <- 1
		go func(infos []UnikFileInfo) {
			defer func() {
				_wg.Done()
				<-_tokens
			}()

			_m := make(map[uint64]struct{}, mapInitSize)

			for _, _info := range infos {
				tokensOpenFiles <- 1

				infh, r, _, err := inStream(_info.Path)
				if err != nil {
					checkError(err)
				}
				defer r.Close()

				reader, err := unikmer.NewReader(infh)
				if err != nil {
					checkError(err)
				}

				var code uint64
				for {
					code, _, err = reader.ReadCodeWithTaxid()
					if err != nil {
						if err == io.EOF {
							break
						}
						checkError(errors.Wrap(err, _info.Path))
					}
					_m[code] = struct{}{}
				}

				<-tokensOpenFiles
			}

			_ch <- int64(len(_m))
		}(infos)
	}
	_wg.Wait()
	close(_ch)
	<-_done

	return
}
