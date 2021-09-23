# CAMI2 database

Dataset homepage: https://data.cami-challenge.org/participate

- [CAMI 2 Challenge NCBI Taxonomy](https://openstack.cebitec.uni-bielefeld.de:8080/swift/v1/CAMI_2_DATABASES/ncbi_taxonomy.tar)
- [CAMI 2 Challenge NCBI RefSeq database](https://openstack.cebitec.uni-bielefeld.de:8080/swift/v1/CAMI_2_DATABASES/RefSeq_genomic_20190108.tar)
- [CAMI 2 Challenge Accession to Taxid Mapping](https://openstack.cebitec.uni-bielefeld.de:8080/swift/v1/CAMI_2_DATABASES/ncbi_taxonomy_accession2taxid.tar)

## Tools

- [seqkit](https://github.com/shenwei356/seqkit)
- [taxonkit](https://github.com/shenwei356/taxonkit)
- [csvtk](https://github.com/shenwei356/csvtk)
- [rush](https://github.com/shenwei356/rush)
- [brename](https://github.com/shenwei356/brename)
- [fd](https://github.com/sharkdp/fd)

## Bacteria and Archaea

In short, we need to use taxonomy data to get mapping relationship between TaxIds and files:

    taxid <- sequence accession <-> file


TaxIds and accessions of archaea (2157), bacteria (2), viral (10239), fungi (4751)

    # taxids
    taxonkit list --ids 2157,2,10239,4751 --indent "" \
        --data-dir taxdump/ -o microbe.taxids.txt
    
    # acc2taxid
    csvtk grep -Ht -f 3 -P microbe.taxids.txt ncbi_taxonomy_accession2taxid/nucl_gb.accession2taxid.gz \
        | csvtk cut -Ht -f 2,3 -o acc2taxid.microbe.tsv

Extract sequences of microbes

    # file2acc
    fd fna.gz refseq/ \
        | rush -k 'echo -ne "{%}\t$(seqkit head -n 1 {} | seqkit seq -ni)\n"' \
        > file2acc.tsv

    # filter
    cat file2acc.tsv \
        | csvtk grep -Ht -f 2 -P <(cut -f 1 acc2taxid.microbe.tsv) \
        > file2acc.microbe.tsv
            
    mkdir -p refseq-cami2
    
    # link to sequences files
    cut -f 1 file2acc.microbe.tsv \
        | rush 'ln -s ../refseq/{} refseq-cami2/{}'
    
    # file2taxid
    csvtk replace -Ht -f 2 -p '(.+)' -r '{kv}' file2acc.microbe.tsv \
        -k <(csvtk grep -Ht -P <(cut -f 2 file2acc.microbe.tsv) acc2taxid.microbe.tsv) \
        > file2taxid.tsv
    
    # ref2taxid    
    cat file2taxid.tsv | csvtk replace -Ht -p '^(\w{3}_\d{9}\.\d+).+' -r '$1' \
        > ref2taxid.tsv
        

Stats (optional)

    cat ref2taxid.tsv \
        | taxonkit lineage -i 2 -r -n -L --data-dir taxdump/ \
        | taxonkit reformat -I 2 -f '{k}\t{p}\t{c}\t{o}\t{f}\t{g}\t{s}' --data-dir taxdump/ \
        | csvtk add-header -t -n 'accession,taxid,name,rank,superkindom,phylum,class,order,family,genus,species' \
        > ref2taxid.lineage.tsv

    # top 10 species
    cat ref2taxid.lineage.tsv \
        | csvtk freq -t -f species -nr \
        | csvtk head -t -n 10 \
        | csvtk pretty -t \
        | tee ref2taxid.lineage.tsv.top10-species.txt

    species                      frequency
    --------------------------   ---------
    Staphylococcus aureus        3631
    Mycobacterium tuberculosis   1856
    Escherichia coli             1734
    Klebsiella pneumoniae        754
    Salmonella enterica          749
    Enterococcus faecalis        373
    Enterococcus faecium         364
    Bordetella pertussis         359
    Pseudomonas aeruginosa       338
    Streptococcus pyogenes       242

Simplified database

    # keep at most 5 assemblies for a species
    cat ref2taxid.tsv \
        | csvtk uniq -Ht -f 2 \
        | taxonkit reformat -I 2 -t -f '{s}' --data-dir taxdump/ \
        | csvtk uniq -Ht -f 4 -n 5 \
        | csvtk cut -Ht -f 1,2 \
        > ref2taxid.slim.tsv
    
    cat ref2taxid.slim.tsv \
        | taxonkit lineage -i 2 -r -n -L --data-dir taxdump/ \
        | taxonkit reformat -I 2 -f '{k}\t{p}\t{c}\t{o}\t{f}\t{g}\t{s}' --data-dir taxdump/ \
        | csvtk add-header -t -n 'accession,taxid,name,rank,superkindom,phylum,class,order,family,genus,species' \
        > ref2taxid.slim.lineage.tsv
    cat ref2taxid.slim.lineage.tsv \
        | csvtk freq -t -f species -nr \
        | csvtk head -t -n 10 \
        | csvtk pretty -t \
        | tee ref2taxid.slim.lineage.tsv.top10-species.txt
    cat ref2taxid.slim.lineage.tsv \
        | csvtk freq -t -f species -nr \
        > ref2taxid.slim.lineage.tsv.n-species.txt
        
    
    mkdir refseq-cami2-slim
    cd refseq-cami2-slim
    
    cat ../file2acc.microbe.tsv \
        | cut -f 1 \
        | csvtk mutate -Ht -p "^(\w{3}_\d{9}\.\d+)" \
        | csvtk grep -Ht -f 2 -P <(cut -f 1 ../ref2taxid.slim.tsv) \
        | cut -f 1 \
        | rush 'ln -s ../refseq-cami2/{}'

Building database
            
    genomes=refseq-cami2-slim/
    
    genomes=${genomes%/}
    
    # mask low-complexity region
    outdir=$genomes.masked
    mkdir -p $outdir
    fd fna.gz $genomes/ \
        | rush -v outdir=$outdir 'dustmasker -in <(zcat {}) -outfmt fasta \
            | sed -e "/^>/!s/[a-z]/n/g" \
            | gzip -c > {outdir}/{%}'
            
    # rename files
    brename -R -p '^(\w{3}_\d{9}\.\d+).+' -r '$1.fna.gz' $outdir

    # -----------------------------------------------------------------
    
    genomes=refseq-cami2-slim.masked/
    genomes=${genomes%/}
    prefix=refseq-cami2
    
    j=40
    
    # for short reads
    k=21
    kmcp compute -I $genomes/ -O $prefix-k$k-n10 \
        -e -k $k -n 10 -B plasmid
        
    n=1
    f=0.3
    kmcp index -I $prefix-k$k-n10/ -O $prefix-k$k.db -j $j -n $n -f $f --dry-run   

## Viruses

    # download
    wget ftp://ftp.ncbi.nlm.nih.gov/genomes/refseq/viral/assembly_summary.txt
    
    # convert to TSV
    cat assembly_summary.txt | sed 1d | sed '1s/^# //' \
        | sed 's/"/$/g' > assembly_summary.tsv
    
    # filter by date
    # 8228
    cat assembly_summary.tsv \
        | csvtk filter2 -t -f '$seq_rel_date < "2019/01/10"' \
        > assembly_summary.cami2.tsv
    
    # download
    genomes=virus
    
    mkdir -p $genomes
    cat assembly_summary.cami2.tsv \
        | csvtk cut -t -f ftp_path \
        | csvtk del-header -t \
        | rush -v 'prefix={}/{%}' -v outdir=$genomes \
            ' wget -c {prefix}_genomic.fna.gz -O {outdir}/{%}_genomic.fna.gz' \
            -j 10 -c -C download.rush
    
   
    # filter by length and mask low-complexity region
    outdir=virus.masked
    mkdir -p $outdir  
    /bin/rm -rf $outdir
    mkdir -p $outdir    
    seqkit stats -j 4 -T --infile-list <(ls $genomes/*) \
        | csvtk filter2 -t -f '$sum_len >= 1000' \
        | csvtk cut -t -f file \
        | csvtk del-header -t \
        | rush -v outdir=$outdir 'dustmasker -in <(zcat {}) -outfmt fasta \
            | sed -e "/^>/!s/[a-z]/n/g" \
            | gzip -c > {outdir}/{%}'
            
    # rename files
    brename -R -p '^(\w{3}_\d{9}\.\d+).+' -r '$1.fna.gz' $outdir
    
    TODO: mapping new TaxId to old taxId
    
    # id -> taxid
    cat assembly_summary.tsv \
        | csvtk grep -t -f assembly_accession -P <(ls $outdir | sed 's/.fna.gz//') \
        | csvtk cut -t -f assembly_accession,taxid \
        | csvtk del-header -t \
        > taxid-virus.map.new
        
    # stats
    cat taxid-virus.map \
        | taxonkit lineage -i 2 -r -n -L --data-dir taxdump/ \
        | taxonkit reformat -I 2 -f '{k}\t{p}\t{c}\t{o}\t{f}\t{g}\t{s}' --data-dir taxdump/ \
        | csvtk add-header -t -n 'accession,taxid,name,rank,superkindom,phylum,class,order,family,genus,species' \
        > taxid-virus.map.lineage.tsv
    cat taxid-virus.map.lineage.tsv \
        | csvtk freq -t -f species -nr \
        > taxid-virus.map.lineage.tsv.n-species.txt
    
    # id -> name
    cat assembly_summary.tsv \
        | csvtk grep -t -f assembly_accession -P <(ls $outdir | sed 's/.fna.gz//') \
        | csvtk cut -t -f assembly_accession,organism_name \
        | csvtk del-header -t \
        > name-virus.map
        
    # create kmcp database
    kmcp compute -k 21 -e -n 5 -l 100 -I virus.masked/ -O refseq-cami2-virus-k21-n5
    
    kmcp index -I refseq-cami2-virus-k21-n5/ -O refseq-cami2-virus-k21-n5.db \
        -j 40 -n 3 -f 0.001 -x 100k
    