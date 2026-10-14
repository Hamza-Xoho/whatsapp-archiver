#!/bin/bash
# usage: mut.sh <name>   -> creates a fresh mutation copy of base (sharing caches) and prints its path
SP=/private/tmp/claude-501/-Users-hamza-Code-whatsapp-archiver/284f1bb0-ebf3-4003-8336-9709f09e28c1/scratchpad
D=$SP/work/$1
rm -rf "$D"
rsync -a --exclude .cache --exclude .tools --exclude .git "$SP/work/base/" "$D/"
mkdir -p "$D/.cache/gopath"
ln -s /Users/hamza/Code/whatsapp-archiver/archiver/.cache/gomod "$D/.cache/gomod"
ln -s /Users/hamza/Code/whatsapp-archiver/archiver/.cache/gocache "$D/.cache/gocache"
ln -s /Users/hamza/Code/whatsapp-archiver/archiver/.tools "$D/.tools"
echo "$D"
