package kv

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"sync"
	"testing"
)

type memoryBlobClient struct {
	mu    sync.Mutex
	blobs map[string]map[string][]byte
	url   string
}

func newTestBlobClient(t testing.TB) (AzureBlobClient, string) {
	if t != nil {
		t.Helper()
	}
	return &memoryBlobClient{
		blobs: make(map[string]map[string][]byte),
		url:   "https://test.blob.core.windows.net/",
	}, "testcontainer"
}

func (c *memoryBlobClient) StoreBlob(ctx context.Context, containerName, blobName string, value []byte) error {
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

func (c *memoryBlobClient) LoadBlob(ctx context.Context, containerName, blobName string) ([]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	container := c.blobs[containerName]
	if container == nil {
		return nil, fmt.Errorf("%w: %s", ErrBlobNotFound, blobName)
	}
	value, ok := container[blobName]
	if !ok {
		return nil, fmt.Errorf("%w: %s", ErrBlobNotFound, blobName)
	}
	return append([]byte(nil), value...), nil
}

func (c *memoryBlobClient) DeleteBlob(ctx context.Context, containerName, blobName string) error {
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

func (c *memoryBlobClient) ListBlobs(ctx context.Context, containerName, prefix string) ([]string, error) {
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

func (c *memoryBlobClient) URL() string { return c.url }

type hidingBlobClient struct {
	AzureBlobClient
	hideContainer string
	hidePrefix    string
	hidden        map[string][]byte
}

func (c *hidingBlobClient) StartHiding(containerName, prefix string) {
	if c.hideContainer != "" {
		panic("already hiding")
	}
	c.hideContainer = containerName
	c.hidePrefix = prefix
	c.hidden = map[string][]byte{}
}

func (c *hidingBlobClient) Unhide() {
	for blobName, data := range c.hidden {
		if err := c.AzureBlobClient.StoreBlob(ctx, c.hideContainer, blobName, data); err != nil {
			panic(err)
		}
	}
	c.hideContainer = ""
	c.hidePrefix = ""
	c.hidden = nil
}

func (c *hidingBlobClient) StoreBlob(ctx context.Context, containerName, blobName string, value []byte) error {
	if c.hideContainer == containerName && strings.HasPrefix(blobName, c.hidePrefix) {
		c.hidden[blobName] = append([]byte(nil), value...)
		return nil
	}
	return c.AzureBlobClient.StoreBlob(ctx, containerName, blobName, value)
}

type countingBlobClient struct {
	AzureBlobClient
	l                    sync.Mutex
	getAttemptCountByKey map[string]int
	putAttemptCount      int
}

func (c *countingBlobClient) LoadBlob(ctx context.Context, containerName, blobName string) ([]byte, error) {
	if c.getAttemptCountByKey == nil {
		c.getAttemptCountByKey = map[string]int{blobName: 1}
	} else {
		c.getAttemptCountByKey[blobName]++
	}
	return c.AzureBlobClient.LoadBlob(ctx, containerName, blobName)
}

func (c *countingBlobClient) StoreBlob(ctx context.Context, containerName, blobName string, value []byte) error {
	c.l.Lock()
	c.putAttemptCount++
	c.l.Unlock()
	return c.AzureBlobClient.StoreBlob(ctx, containerName, blobName, value)
}
