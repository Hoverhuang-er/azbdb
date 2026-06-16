package azb

import (
	"context"
	"database/sql"
	"fmt"
	"sort"
	"strings"
	"sync"
	"testing"

	coreazb "github.com/Hoverhuang-er/azbdb/pkg/azb"
	"github.com/Hoverhuang-er/azbdb/pkg/kv"
	"github.com/stretchr/testify/require"
)

const testConnectionString = "DefaultEndpointsProtocol=https;AccountName=test;AccountKey=test;EndpointSuffix=core.windows.net;ContainerName=testcontainer"

func TestNewDBCreatesTablesUnderSQLiteWALPrefix(t *testing.T) {
	ctx := context.Background()
	client := installMemoryAzure(t)

	db, err := NewDB(ctx, testConnectionString)
	require.NoError(t, err)
	defer db.Close()

	require.NoError(t, CreateTable(ctx, db, "items", "id primary key, value"))
	require.NoError(t, CreateTable(ctx, db, "items", "id primary key, value"))
	_, err = db.ExecContext(ctx, `INSERT INTO items VALUES (?, ?)`, 1, "one")
	require.NoError(t, err)

	rows := queryRows(t, db, `SELECT id, value FROM items`)
	require.Equal(t, [][]any{{int64(1), "one"}}, rows)

	blobs, err := client.ListBlobs(ctx, "testcontainer", "sqlite_wal/")
	require.NoError(t, err)
	require.Contains(t, blobs, "sqlite_wal/.azb")
	require.True(t, containsPrefix(blobs, "sqlite_wal/items/azb-rows/"), blobs)
}

func TestNewReadOnlyDBReadsAndRejectsWrites(t *testing.T) {
	ctx := context.Background()
	installMemoryAzure(t)

	writer, err := NewDB(ctx, testConnectionString)
	require.NoError(t, err)
	defer writer.Close()
	require.NoError(t, CreateTable(ctx, writer, "items", "id primary key, value"))
	_, err = writer.ExecContext(ctx, `INSERT INTO items VALUES (?, ?)`, 1, "one")
	require.NoError(t, err)

	reader, err := NewReadOnlyDB(ctx, testConnectionString)
	require.NoError(t, err)
	defer reader.Close()
	require.NoError(t, CreateTable(ctx, reader, "items", "id primary key, value"))
	require.Equal(t, [][]any{{int64(1), "one"}}, queryRows(t, reader, `SELECT id, value FROM items`))

	_, err = reader.ExecContext(ctx, `INSERT INTO items VALUES (?, ?)`, 2, "two")
	require.ErrorContains(t, err, "opened as read-only")
}

func TestNewDBAllowsSameTableNameAcrossHandles(t *testing.T) {
	ctx := context.Background()
	installMemoryAzure(t)

	writer1, err := NewDB(ctx, testConnectionString)
	require.NoError(t, err)
	defer writer1.Close()
	writer2, err := NewDB(ctx, testConnectionString)
	require.NoError(t, err)
	defer writer2.Close()

	require.NoError(t, CreateTable(ctx, writer1, "items", "id primary key, value"))
	require.NoError(t, CreateTable(ctx, writer2, "items", "id primary key, value"))
	_, err = writer1.ExecContext(ctx, `INSERT INTO items VALUES (?, ?)`, 1, "one")
	require.NoError(t, err)
	_, err = writer2.ExecContext(ctx, `INSERT INTO items VALUES (?, ?)`, 2, "two")
	require.NoError(t, err)

	reader, err := NewReadOnlyDB(ctx, testConnectionString)
	require.NoError(t, err)
	defer reader.Close()
	require.NoError(t, CreateTable(ctx, reader, "items", "id primary key, value"))
	require.Equal(t, [][]any{{int64(1), "one"}, {int64(2), "two"}}, queryRows(t, reader, `SELECT id, value FROM items ORDER BY id`))
}

func TestNewDBRequiresContainer(t *testing.T) {
	_, err := NewDB(context.Background(), "DefaultEndpointsProtocol=https;AccountName=test;AccountKey=test;EndpointSuffix=core.windows.net")
	require.ErrorContains(t, err, "ContainerName")
}

func queryRows(t *testing.T, db queryer, query string) [][]any {
	t.Helper()
	rows, err := db.QueryContext(context.Background(), query)
	require.NoError(t, err)
	defer rows.Close()

	cols, err := rows.Columns()
	require.NoError(t, err)
	out := [][]any{}
	for rows.Next() {
		row := make([]any, len(cols))
		dest := make([]any, len(cols))
		for i := range row {
			dest[i] = &row[i]
		}
		require.NoError(t, rows.Scan(dest...))
		out = append(out, row)
	}
	require.NoError(t, rows.Err())
	return out
}

type queryer interface {
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
}

func installMemoryAzure(t *testing.T) *memoryBlobClient {
	t.Helper()
	client := &memoryBlobClient{blobs: make(map[string]map[string][]byte), url: "https://test.blob.core.windows.net/"}
	restore := coreazb.SetAzureBlobClientFactoryForTesting(func(kv.AzureBlobClientOptions) (kv.AzureBlobClient, error) {
		return client, nil
	})
	t.Cleanup(restore)
	return client
}

type memoryBlobClient struct {
	mu    sync.Mutex
	blobs map[string]map[string][]byte
	url   string
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
		return nil, fmt.Errorf("%w: %s", kv.ErrBlobNotFound, blobName)
	}
	value, ok := container[blobName]
	if !ok {
		return nil, fmt.Errorf("%w: %s", kv.ErrBlobNotFound, blobName)
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

func containsPrefix(values []string, prefix string) bool {
	for _, value := range values {
		if strings.HasPrefix(value, prefix) {
			return true
		}
	}
	return false
}
