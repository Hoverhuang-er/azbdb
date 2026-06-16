package main

import (
	_ "github.com/Hoverhuang-er/azbdb/pkg/sqlite"
)

//go:generate sh -c "go build -buildmode=c-archive -o azb.a"

// placeholder for c-archive
func main() {}
