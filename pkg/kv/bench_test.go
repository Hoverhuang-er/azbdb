package kv

import (
	"context"
	"testing"
	"time"
)

func BenchmarkSet1(b *testing.B) {
	ctx := context.Background()
	t, close := newTestTree(0, 0)
	defer close()
	for n := 0; n < b.N; n++ {
		t.Set(ctx, time.Time{}, 0, 0)
	}
	t.Cancel()
}

func BenchmarkSetN(b *testing.B) {
	ctx := context.Background()
	t, close := newTestTree(0, 0)
	defer close()
	for n := 0; n < b.N; n++ {
		t.Set(ctx, time.Time{}, n, n)
	}
	t.Cancel()
}

func BenchmarkSetNCommit(b *testing.B) {
	ctx := context.Background()
	t, close := newTestTree(0, 0)
	defer close()
	for n := 0; n < b.N; n++ {
		t.Set(ctx, time.Time{}, n, n)
	}
	t.Commit(ctx)
}

func BenchmarkGet1(b *testing.B) {
	ctx := context.Background()
	t, close := newTestTree(0, 0)
	defer close()
	t.Set(ctx, time.Time{}, 0, 0)
	var v int
	for n := 0; n < b.N; n++ {
		t.Get(ctx, n, &v)
	}
	t.Cancel()
}

func BenchmarkGetNMemory(b *testing.B) {
	ctx := context.Background()
	t, close := newTestTree(0, 0)
	defer close()
	for n := 0; n < b.N; n++ {
		t.Set(ctx, time.Time{}, n, n)
	}
	t.Cancel()
	var v int
	for n := 0; n < b.N; n++ {
		t.Get(ctx, n, &v)
	}
}

func BenchmarkGetNStored(b *testing.B) {
	ctx := context.Background()
	t, close := newTestTree(0, 0)
	defer close()
	for n := 0; n < b.N; n++ {
		t.Set(ctx, time.Time{}, n, n)
	}
	t.Commit(ctx)
	var v int
	for n := 0; n < b.N; n++ {
		t.Get(ctx, n, &v)
	}
}

func newTestTree(zeroKey, zeroValue interface{}) (*DB, func()) {
	ctx := context.Background()
	c, containerName := newTestBlobClient(nil)

	cfg := Config{
		Storage: &AzureBlobStorageInfo{
			ServiceURL:    c.URL(),
			ContainerName: containerName,
			Prefix:        "/my-awesome-database",
		},
		KeysLike:   "key",
		ValuesLike: 1234,
	}
	s, err := Open(ctx, c, cfg, OpenOptions{}, time.Now())
	if err != nil {
		panic(err)
	}
	return s, func() {}
}
