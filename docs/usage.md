# Usage

KMCP is a command-line tool consisting of several subcommands.

```text
    Program: kmcp (K-mer-based Metagenomic Classification and Profiling)
    Version: v0.6.0
  Documents: https://shenwei356.github.io/kmcp
Source code: https://github.com/shenwei356/kmcp

kmcp is a tool for metagenomic classification and profiling.

Usage:
  kmcp [command]

Available Commands:
  autocomplete Generate shell autocompletion script
  compute      Generate k-mers (sketch) from FASTA/Q sequences
  help         Help about any command
  index        Construct database from k-mer files
  info         Print information of index file
  merge        Merge search results from multiple databases
  profile      Generate taxonomic profile from search results
  search       Search sequence against a database
  version      Print version information and check for update

Flags:
  -h, --help                 help for kmcp
  -i, --infile-list string   file of input files list (one file per line), if given, they are appended to files from cli arguments
      --log string           log file
  -q, --quiet                do not print any verbose information. you can write them to file with --log
  -j, --threads int          number of CPUs to use (default 16)

Use "kmcp [command] --help" for more information about a command.

```


## compute

```text
Generate k-mers (sketchs) from FASTA/Q sequences

Attentions:
  1. Input files can be given as list of FASTA/Q files via
     positional arguments or a directory containing sequence files
     via the flag -I/--in-dir. A regular expression for matching
     sequencing files is available by the flag -r/--file-regexp.
  2. Multiple sizes of k-mers are supported.
  3. By default, we compute k-mers (sketches) of every file,
     you can also use --by-seq to compute for every sequence,
     where sequence IDs in all input files better be distinct.
  4. Unwanted sequence like plasmid can be filtered out by
     the name via regular expressions (-B/--seq-name-filter).
  5. It also supports splitting sequences into fragments, this
     could increase the specificity in profiling result in cost
     of slower searching speed.

Supported k-mer (sketches) types:
  1. K-mer:
     1). ntHash of k-mer (-k)
  2. K-mer sketchs (all using ntHash):
     1). Scaled MinHash (-k -D)
     2). Minimizer      (-k -W), optionally scaling/down-sampling (-D)
     3). Closed Syncmer (-k -S), optionally scaling/down-sampling (-D)

Splitting sequences:
  1. Sequences can be splitted into fragments by a fragment size 
     (-s/--split-size) or number of fragments (-n/--split-number)
     with overlap (-l/--split-overlap).
  2. When splitting by number of fragments, all sequences (except for
     these mathching any regular expression given by -B/--seq-name-filter)
     in a sequence file are concatenated before splitting.
  3. Both sequence IDs and fragments indices are saved for later use,
     in form of meta/description data in .unik files.

Meta data:
  1. Every outputted .unik file contains the sequence ID/reference name,
     fragment index, number of fragments, and genome size of reference.
  2. When parsing whole sequence files or splitting by number of fragments,
     the identifier of a reference is the basename of the input file
     by default. It can also be extracted from the input file name via
     -N/--ref-name-regexp, e.g., "^(\w{3}_\d{9}\.\d+)" for refseq records.

Output:
  1. All outputted .unik files are saved in ${outdir}, with path
     ${outdir}/xxx/yyy/zzz/${infile}-id_${seqID}.unik
     where dirctory tree '/xxx/yyy/zzz/' is built for > 1000 output files.
  2. For splitting sequence mode (--split-size > 0 or --split-number > 0),
     output files are:
     ${outdir}//xxx/yyy/zzz/${infile}/{seqID}-frag_${fragIdx}.unik

Tips:
  1. Decrease value of -j/--threads for data in hard disk drives to
     reduce I/O pressure.

Usage:
  kmcp compute [flags]

Flags:
      --by-seq                    compute k-mers (sketches) for every sequence, instead of whole file
      --circular                  input sequence is circular
  -c, --compress                  output gzipped .unik files, it's slower and can saves little space
  -r, --file-regexp string        regular expression for matching files in -I/--in-dir to compute, case ignored (default "\\.(f[aq](st[aq])?|fna)(.gz)?$")
      --force                     overwrite output directory
  -h, --help                      help for compute
  -I, --in-dir string             directory containing FASTA/Q files. directory symlinks are followed
  -k, --kmer ints                 k-mer size(s) (default [21])
  -W, --minimizer-w int           minimizer window size
  -O, --out-dir string            output directory
  -N, --ref-name-regexp string    regular expression (must contains "(" and ")") for extracting reference name from file name
  -D, --scale int                 scale/down-sample factor (default 1)
  -B, --seq-name-filter strings   list of regular expressions for filtering out sequences by header/name, case ignored
  -m, --split-min-ref int         only splitting sequences >= M bp (default 1000)
  -n, --split-number int          fragment number, incompatible with -s/--split-size
  -l, --split-overlap int         fragment overlap for splitting sequences
  -s, --split-size int            fragment size for splitting sequences, incompatible with -n/--split-number
  -S, --syncmer-s int             bounded syncmer length
```

Example


## index

```text
Construct database from k-mer files

We build index for k-mers (sketches) with a modified compact bit-sliced
signature index (COBS) or optional with a repeated and merged bloom
filter (RAMBO). We totally rewrite the algorithms and try to combine
their advantages.

Attentions:
  1. All input .unik files should be generated by "kmcp compute".

Tips:
  1. Value of block size -b/--block-size better be multiple of 64.
     The default values is:  (#unikFiles/#threads + 7) / 8 * 8
  2. #threads files are simultaneously opened, and max number
     of opened files is limited by the flag -F/--max-open-files.
     You may use a small value of -F/--max-open-files for 
     hard disk drive storage.
 *3. Use --dry-run to adjust parameters and check final number of 
     index files (#index-files) and the total file size.

Database size and searching accuracy:
  0. Use --dry-run before starting creating database.
  1. -f/--false-positive-rate: the default value 0.3 is enough for a
     query with tens of matched k-mers (see BIGSI/COBS paper).
     Small values could largely increase the size of database.
  2. -n/--num-hash: large values could reduce the database size,
     in cost of slower searching speed. Values <=4 is recommended.
  3. Use flag -x/--block-sizeX-kmers-t, -8/--block-size8-kmers-t,
     and -1/--block-size1-kmers-t to separately create index for
     inputs with huge number of k-mers, for precise control of
     database size.

Repeated and merged bloom filter (RAMBO)
  1. It's optional with flags -R/--num-repititions and -B/--num-buckets.
     Values of these flags should be carefully chosen after testing.
  2. For less than tens of thousands input files, these's no need to
     use RAMBO index, which has much bigger database files and
     slightly higher false positive rate.

References:
  1. COBS: https://arxiv.org/abs/1905.09624
  2. RAMBO: https://arxiv.org/abs/1910.02611

Taxonomy data:
  1. No taxonomy data are included in the database.
  2. Taxonomy information are only needed in "profile" command.

Usage:
  kmcp index [flags]

Flags:
  -a, --alias string                 database alias/name, default: basename of --out-dir. you can also manually edit it in info file: ${outdir}/__db.yml
  -b, --block-size int               block size, better be multiple of 64 for large number of input files. default: min(#.files/#theads, 8)
  -1, --block-size1-kmers-t string   if k-mers of single .unik file exceeds this threshold, an individual index is created for this file. unit supported: K, M, G (default "200M")
  -8, --block-size8-kmers-t string   if k-mers of single .unik file exceeds this threshold, block size is changed to 8. unit supported: K, M, G (default "20M")
  -X, --block-sizeX int              if k-mers of single .unik file exceeds --block-sizeX-kmers-t, block size is changed to this value (default 256)
  -x, --block-sizeX-kmers-t string   if k-mers of single .unik file exceeds this threshold, block size is changed to --block-sizeX. unit supported: K, M, G (default "10M")
      --dry-run                      dry run, useful for adjusting parameters (recommended)
  -f, --false-positive-rate float    false positive rate of single bloom filter, range: (0, 1) (default 0.3)
      --file-regexp string           regular expression for matching files in -I/--in-dir to index, case ignored (default ".unik$")
      --force                        overwrite output directory
  -h, --help                         help for index
  -I, --in-dir string                directory containing .unik files. directory symlinks are followed
  -F, --max-open-files int           maximum number of opened files, please use a small value for hard disk drive storage (default 256)
  -B, --num-buckets int              [RAMBO] number of buckets per repitition, 0 for one set per bucket
  -n, --num-hash int                 number of hashes of bloom filters (default 1)
  -R, --num-repititions int          [RAMBO] number of repititions (default 1)
  -O, --out-dir string               output directory. default: ${indir}.kmcp-db
      --seed int                     [RAMBO] seed for randomly assigning names to buckets (default 1)

```

## search

```text
Search sequence against a database

Attentions:
  1. A long query sequences may contain duplicated k-mers, which are
     not removed for short sequences by default. You may modify the
     value of -u/--kmer-dedup-threshold to remove duplicates.
  2. Input format should be (gzipped) FASTA or FASTQ from files or stdin.
  3. Increase value of -j/--threads for acceleratation, but values larger
     than 2 * number of index files (.uniki) won't bring extra speedup.

Shared flags between "search" and "profile":
  1. -t/--min-query-cov.
  2. -n/--keep-top-scores, here it can reduce the output size, while
     it does not effect the speed.
  3. -N/--name-map.

Special attentions:
  1. The values of tCov and jacc only apply for single size of k-mer.

Usage:
  kmcp search [flags]

Flags:
  -d, --db-dir string              database directory created by "kmcp index"
  -D, --default-name-map           load ${db}/__name_mapping.tsv for mapping name first
  -S, --do-not-sort                do not sort matches
  -h, --help                       help for search
  -n, --keep-top-scores int        keep matches with the top N score for a query, 0 for all (default 5)
  -K, --keep-unmatched             keep unmatched query sequence information
  -u, --kmer-dedup-threshold int   remove duplicated kmers for a query with >= N k-mers (default 256)
      --low-mem                    do not load all index files into memory, the searching would be very very slow
  -c, --min-kmers int              minimal number of matched k-mers (sketches) (default 10)
  -t, --min-query-cov float        minimal query coverage, i.e., proportion of matched k-mers and unique k-mers of a query (default 0.6)
  -m, --min-query-len int          minimal query length (default 70)
  -T, --min-target-cov float       minimal target coverage, i.e., proportion of matched k-mers and unique k-mers of a target
  -N, --name-map strings           tabular two-column file(s) mapping names to user-defined values
  -H, --no-header-row              do not print header row
  -o, --out-file string            out file, supports and recommends a ".gz" suffix ("-" for stdout) (default "-")
  -g, --query-whole-file           use the whole file as query
  -s, --sort-by string             sort hits by "qcov" (Containment Index), "tcov" or "jacc" (Jaccard Index) (default "qcov")

```

## merge

```text
Merge search results from multiple databases

Attentions
  0. Input files should contain queryIdx field.
  1. Referene IDs should be distinct accross all databases.

Usage:
  kmcp merge [flags]

Flags:
  -h, --help                 help for merge
  -H, --no-header-row        do not print header row
  -o, --out-file string      out file, supports and recommends a ".gz" suffix ("-" for stdout) (default "-")
  -f, --queryIdx-field int   field of queryIdx (default 15)

```

## profile


```text
Generate taxonomic profile from search results

Methods:
  1. We use the two-stage taxonomy assignment algorithm in MegaPath
     to reduce the false positive of ambiguous matches.
  2. Multi-aligned queries are proportionally assigned to references
     with the strategy in Metalign.
  4. More strategies are adopted to increase accuracy.
  5. Reference genomes can be splitted into fragments when computing
     k-mers (sketches), which could help to increase the specificity
     via a threshold, i.e., the minimal proportion of matched fragments
     (-p/--min-frags-prop).
  6. Input files are parsed 3 times, therefore STDIN is not supported.

Reference:
  1. MegaPath: https://doi.org/10.1186/s12864-020-06875-6
  2. Metalign: https://doi.org/10.1186/s13059-020-02159-0

Accuracy notes:
  *. Smaller -t/--min-qcov increase sensitivity in cost of higher false
     positive rate (-f/--max-fpr) of a query.
  *. And we require part of the uniquely matched reads of a reference
     having high similarity, i.e., with high confidence to decrease
     the false positive.
     E.g., H >= 0.8 and -P >= 0.1 equals to 90th percentile >= 0.8
     *. -U/--min-hic-ureads,      minimal number, >= 1
     *. -H/--min-hic-ureads-qcov, minimal query coverage, >= -t/--min-qcov
     *. -P/--min-hic-ureads-prop, minimal proportion, higher values
        increase precision in cost of sensitivity.
  *. -n/--keep-top-qcovs could increase the speed, while too small value
     could decrease the sensitivity.
  *. -R/--max-mismatch-err and -D/--min-dreads-prop is for determing
     the right reference for ambigous reads.
  *. --keep-perfect-match is not recommended, which decreases sensitivity.  

Taxonomy data:
  1. Mapping references IDs to TaxIds: -T/--taxid-map
  2. NCBI taxonomy dump files: -X/--taxdump

Performance notes:
  1. Searching results are parsed in parallel, and the number of
     lines proceeded by a thread can be set by the flag --chunk-size.
  2. However using a lot of threads does not always accelerate
     processing, 4 threads with chunk size of 500-5000 is fast enough.

Profiling output formats:
  1. kmcp
  2. CAMI         (-M/--metaphlan-report)
  3. MetaPhlAn v2 (-C/--cami-report)

Usage:
  kmcp profile [flags]

Flags:
  -B, --binning-result string       save extra binning result in CAMI report
  -C, --cami-report string          save extra CAMI-like report
      --chunk-size int              number of lines to process for each thread, and 4 threads is fast enough. Type "kmcp profile -h" for details (default 5000)
  -F, --filter-low-pct float        filter out predictions with the smallest relative abundances summing up N%. Range: [0,100)
  -h, --help                        help for profile
  -m, --keep-main-match             only keep main matches, abandon matches with sharply decreased qcov (> --max-qcov-gap)
      --keep-perfect-match          only keep the perfect matches (qcov == 1) if there are
  -n, --keep-top-qcovs int          keep matches with the top N qcovs for a query, 0 for all (default 5)
      --level string                level to estimate abundance at. available values: species, strain (default "species")
  -f, --max-fpr float               maximal false positive rate of a read in search result (default 0.01)
  -R, --max-mismatch-err float      maximal error rate of a read being matched to a wrong reference, for determing the right reference for ambiguous reads. Range: (0, 1) (default 0.05)
      --max-qcov-gap float          max qcov gap between adjacent matches (default 0.2)
  -M, --metaphlan-report string     save extra metaphlan-like report
  -D, --min-dreads-prop float       minimal proportion of distinct reads, for determing the right reference for ambiguous reads. Range: (0, 1) (default 0.05)
  -p, --min-frags-prop float        minimal proportion of matched reference fragments (default 0.8)
  -U, --min-hic-ureads int          minimal number of high-confidence uniquely matched reads for a reference (default 1)
  -P, --min-hic-ureads-prop float   minimal proportion of high-confidence uniquely matched reads (default 0.1)
  -H, --min-hic-ureads-qcov float   minimal query coverage of high-confidence uniquely matched reads (default 0.8)
  -t, --min-query-cov float         minimal query coverage of a read in search result (default 0.6)
  -r, --min-reads int               minimal number of reads for a reference fragment (default 50)
  -u, --min-uniq-reads int          minimal number of uniquely matched reads for a reference fragment (default 10)
  -N, --name-map strings            tabular two-column file(s) mapping reference IDs to reference names
      --norm-abund string           method for normalize abundance of a reference by the mean/min/max abundance in all fragments, available values: mean, min, max (default "mean")
  -o, --out-prefix string           out file prefix ("-" for stdout) (default "-")
      --rank-prefix strings         prefixes of taxon name in certain ranks, used with --metaphlan-report  (default [k__,p__,c__,o__,f__,g__,s__,t__])
  -s, --sample-id string            sample ID in result file
  -S, --separator string            separator of TaxIds and taxonomy names (default ";")
      --show-rank strings           only show TaxIds and names of these ranks (default [superkingdom,phylum,class,order,family,genus,species,strain])
  -X, --taxdump string              directory of NCBI taxonomy dump files: names.dmp, nodes.dmp, optional with merged.dmp and delnodes.dmp
  -T, --taxid-map strings           tabular two-column file(s) mapping reference IDs to TaxIds

```

## info

```text
Print information of index file

Usage:
  kmcp info [flags]

Flags:
  -a, --all                 all information
  -b, --basename            only output basenames of files
  -h, --help                help for info
  -o, --out-prefix string   out file prefix ("-" for stdout) (default "-")

```

## autocomplete

```text
Generate shell autocompletion script

Supported shell: bash|zsh|fish|powershell

Bash:

    # generate completion shell
    kmcp genautocomplete --shell bash

    # configure if never did.
    # install bash-completion if the "complete" command is not found.
    echo "for bcfile in ~/.bash_completion.d/* ; do source \$bcfile; done" >> ~/.bash_completion
    echo "source ~/.bash_completion" >> ~/.bashrc

Zsh:

    # generate completion shell
    kmcp genautocomplete --shell zsh --file ~/.zfunc/_kmcp

    # configure if never did
    echo 'fpath=( ~/.zfunc "${fpath[@]}" )' >> ~/.zshrc
    echo "autoload -U compinit; compinit" >> ~/.zshrc

fish:

    kmcp genautocomplete --shell fish --file ~/.config/fish/completions/kmcp.fish

Usage:
  kmcp autocomplete [flags]

Flags:
      --file string    autocompletion file (default "/home/shenwei/.bash_completion.d/kmcp.sh")
  -h, --help           help for autocomplete
      --shell string   autocompletion type (bash|zsh|fish|powershell) (default "bash")

```