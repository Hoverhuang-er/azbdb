package main

import (
	_ "github.com/Hoverhuang-er/azbdb/pkg/sqlite"
)

//go:generate sh -c "go build -buildmode=c-shared -o `if [ \"$GOOS\" = \"darwin\" ] ; then echo azb.dylib ; else echo azb.so ; fi`"

// placeholder for c-shared
func main() {}
