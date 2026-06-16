package mod_test

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"sync"

	"github.com/Hoverhuang-er/azbdb/pkg/azb"
	"github.com/Hoverhuang-er/azbdb/pkg/kv"
)

var sqliteAzureClient = &sqliteMemoryBlobClient{
	blobs: make(map[string]map[string][]byte),
	url:   "https://test.blob.core.windows.net/",
}

func init() {
	azb.SetAzureBlobClientFactoryForTesting(func(kv.AzureBlobClientOptions) (kv.AzureBlobClient, error) {
		return sqliteAzureClient, nil
	})
}

type sqliteMemoryBlobClient struct {
	mu    sync.Mutex
	blobs map[string]map[string][]byte
	url   string
}

func (c *sqliteMemoryBlobClient) StoreBlob(ctx context.Context, containerName, blobName string, value []byte) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	container := c.blobs[containerName]
	if container == nil {
		container = make(map[string][]byte)
		c.blobs[containerName] = container
	}
	container[blobName] = append([]byte(nil), value...)
	return nil
}

func (c *sqliteMemoryBlobClient) LoadBlob(ctx context.Context, containerName, blobName string) ([]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	container := c.blobs[containerName]
	if container == nil {
		return nil, fmt.Errorf("%w: %s", kv.ErrBlobNotFound, blobName)
	}
	value, ok := container[blobName]
	if !ok {
		return nil, fmt.Errorf("%w: %s", kv.ErrBlobNotFound, blobName)
	}
	return append([]byte(nil), value...), nil
}

func (c *sqliteMemoryBlobClient) DeleteBlob(ctx context.Context, containerName, blobName string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if container := c.blobs[containerName]; container != nil {
		delete(container, blobName)
	}
	return nil
}

func (c *sqliteMemoryBlobClient) ListBlobs(ctx context.Context, containerName, prefix string) ([]string, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	container := c.blobs[containerName]
	res := make([]string, 0, len(container))
	for name := range container {
		if strings.HasPrefix(name, prefix) {
			res = append(res, name)
		}
	}
	sort.Strings(res)
	return res, nil
}

func (c *sqliteMemoryBlobClient) URL() string { return c.url }
