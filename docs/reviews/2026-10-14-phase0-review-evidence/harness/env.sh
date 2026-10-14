# usage: source env.sh <repo-dir>
R="$1"
export PATH="/Users/hamza/Code/whatsapp-archiver/archiver/.tools/bin:/Users/hamza/Code/whatsapp-archiver/archiver/.tools/go/bin:$PATH"
export GOPATH="$R/.cache/gopath" GOMODCACHE="$R/.cache/gomod" GOCACHE="$R/.cache/gocache" GOBIN="$R/.tools/bin" GOTOOLCHAIN=auto GOFLAGS=-mod=mod
unset GOFLAGS
