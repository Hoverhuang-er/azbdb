package kv

import (
	"context"
	"fmt"
	"time"

	"github.com/hoverhuang/mast"
)

func Example() {
	ctx := context.Background()
	c, containerName := newTestBlobClient(nil)

	cfg := Config{
		Storage: &AzureBlobStorageInfo{
			ServiceURL:    c.URL(),
			ContainerName: containerName,
			Prefix:        "/my-awesome-database",
		},
		KeysLike:      "key",
		ValuesLike:    1234,
		NodeCache:     mast.NewNodeCache(1024),
		NodeEncryptor: V1NodeEncryptor([]byte("This is a secret passphrase if ever there was one.")),
	}
	s, err := Open(ctx, c, cfg, OpenOptions{}, time.Now())
	if err != nil {
		panic(err)
	}

	// setting a value
	err = s.Set(ctx, time.Now(), "hello", 5)
	if err != nil {
		panic(err)
	}

	// getting a value
	var v int
	ok, err := s.Get(ctx, "hello", &v)
	if err != nil {
		panic(err)
	}
	if !ok {
		panic("not ok")
	}
	fmt.Printf("got %v\n", v)

	// committing the value
	_, err = s.Commit(ctx)
	if err != nil {
		panic(err)
	}

	fmt.Printf("size %d\n", s.Size())
	// Output:
	// got 5
	// size 1
}
