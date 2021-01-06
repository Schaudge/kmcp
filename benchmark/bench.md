## Introduction

Softwares

    - [cobs](https://github.com/bingmann/cobs) ([1915fc0](https://github.com/bingmann/cobs/commit/1915fc061bbe47946116b4a051ed7b4e3f3eca15))
    - [sourmash](https://github.com/dib-lab/sourmash) (v3.5.0)
    - kmcp (v0.2.1)
    - [memusg](https://github.com/shenwei356/memusg), version: [91a19ab](https://github.com/shenwei356/memusg/commit/91a19abaf041c3046b91ef3a35ed28aade1e05fc)

## Dataset

GTDB r95
    - gtdb_genomes_reps_r95.tar.gz(https://data.ace.uq.edu.au/public/gtdb/data/releases/release95/95.0/genomic_files_reps/gtdb_genomes_reps_r95.tar.gz)
    - file size: 32.3 GB
    - #files: 31,910

### sequencing data

PacBio

- HMP mock dataset
    - https://github.com/PacificBiosciences/DevNet/wiki/Human_Microbiome_Project_MockB_Shotgun

ONT

- Zymo mock community
    - http://refhub.elsevier.com/S2589-0042(20)30408-9/sref30
    - https://github.com/LomanLab/mockcommunity
- HM-276D & HM-277D
    - https://doi.org/10.1016/j.isci.2020.101223
    - https://www.ncbi.nlm.nih.gov/bioproject/630658



## Whole sequence

    # indexing ---------------------------------------------------------------------------------
    
    seqs=gtdb # directory of gtdb sequences
    db=gtdb
    k=31
    threads=32
    
    dbCOBS=gtdb-cobs-k$k.cobs_compact
    dbKMCPtmp=gtdb-kmcp-k$k
    dbKMCP=gtdb-kmcp-k$k.db
    
    
    # --------------- cobs ---------------
     
    # 51min, 103G
    /bin/rm -rf $dbCOBS $seqs/*.cobs_cache 
    memusg -t -s "cobs compact-construct -T $threads -k $k -f 0.3 --num-hashes 1 -p 512 --file-type fasta $seqs $dbCOBS --clobber" \
        2>$dbCOBS.time
    
    # 124G
    du -sh $dbCOBS > $dbCOBS.size
    
    
    # --------------- kmcp ---------------
    
    # 13min, 40G
    /bin/rm -rf $dbKMCPtmp $dbKMCP
    memusg -t -s "kmcp compute -j $threads -k $k -I $seqs -O $dbKMCPtmp --force \
        && kmcp index -j $threads -f 0.3 -n 1 -b 512 -I $dbKMCPtmp -O $dbKMCP --force " \
        2>$dbKMCP.time

    # 55G
    du -sh $dbKMCP > $dbKMCP.size
    
   
    # searching  ---------------------------------------------------------------------------------

    t=0.8
    for f in test/*.fa; do
        # 8min, 51G
        memusg -t -s "cobs query  -T $threads -i $dbCOBS -f $f -t $t > $f.cobs@$db.txt" 2>$f.cobs@$db.txt.time
        
        # 10s, 54G
        memusg -t -s "kmcp search -j $threads -d $dbKMCP    $f -t $t > $f.kmcp@$db.txt" 2>$f.kmcp@$db.txt.time
    done

## Scaled minhash

    # indexing ---------------------------------------------------------------------------------

    seqs=gtdb-seqs
    db=gtdb
    k=31
    threads=32
    scale=2000
    
    dbSOURMASHtmp=gtdb-sourmash-k$k-D$scale
    dbSOURMASH=gtdb-sourmash-k$k-D$scale/_db.sbt.json
    dbKMCPtmp=gtdb-kmcp-k$k-D$scale
    dbKMCP=gtdb-kmcp-k$k-D$scale.db
    
    
    # --------------- sourmash ---------------

    mkdir -p $dbSOURMASHtmp
    indexSourmash() {
        ls $seqs/*.fa.gz \
            | rush -j $threads -v d=$dbSOURMASHtmp -v s=$scale -v k=$k \
                'sourmash compute -q --scaled {s} -k {k} {} -o {d}/{%}.sig'     
        sourmash index $dbSOURMASH $dbSOURMASHtmp/*.sig
    }
    
    time indexSourmash 2> $dbSOURMASH.time
    
    # --------------- kmcp ---------------
        
    memusg -t -s "kmcp compute -j $threads -k $k -I $seqs -O $dbKMCPtmp -D $scale --force \
        && kmcp index -j $threads -f 0.3 -n 1 -b 512 -I $dbKMCPtmp -O $dbKMCP --force " \
        2>$dbKMCP.time
    
    
    # searching  ---------------------------------------------------------------------------------
    
    t=0.8
    for f in test/*.fa; do
        sourmash compute -q --scaled $scale -k $k $f -o $f.sig
        memusg -t -s sourmash search $f.sig $dbSOURMASH  --threshold $t > $f.sourmash@$db.txt 2>$f.sourmashs@$db.txt.time
        
        memusg -t -s kmcp search -j $threads -d $dbKMCP    $f -t $t > $f.kmcp@$db.txt 2>$f.kmcp@$db.txt.time
    done