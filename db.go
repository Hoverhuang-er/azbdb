package azb

import (
	"context"
	"database/sql"
	"fmt"
	"strconv"
	"strings"
	"sync/atomic"

	"github.com/Hoverhuang-er/azbdb/internal/azbmetrics"
	coreazb "github.com/Hoverhuang-er/azbdb/pkg/azb"

	_ "github.com/Hoverhuang-er/azbdb/pkg/sqlite"
	_ "github.com/Hoverhuang-er/azbdb/pkg/sqlite/sqlite-autoload-extension"
	_ "github.com/mattn/go-sqlite3"
)

var memoryDBID atomic.Uint64

type DBOption func(*dbConfig)

type dbConfig struct {
	container        string
	prefix           string
	sqliteDSN        string
	readOnly         bool
	entriesPerNode   int
	nodeCacheEntries int
}

func WithContainer(container string) DBOption {
	return func(cfg *dbConfig) { cfg.container = container }
}

func WithPrefix(prefix string) DBOption {
	return func(cfg *dbConfig) { cfg.prefix = prefix }
}

func WithSQLiteDSN(dsn string) DBOption {
	return func(cfg *dbConfig) { cfg.sqliteDSN = dsn }
}

func WithReadOnly(readOnly bool) DBOption {
	return func(cfg *dbConfig) { cfg.readOnly = readOnly }
}

func ReadOnly() DBOption { return WithReadOnly(true) }

func WithEntriesPerNode(entries int) DBOption {
	return func(cfg *dbConfig) { cfg.entriesPerNode = entries }
}

func WithNodeCacheEntries(entries int) DBOption {
	return func(cfg *dbConfig) { cfg.nodeCacheEntries = entries }
}

func NewDB(ctx context.Context, connectionString string, options ...DBOption) (*sql.DB, error) {
	cfg, err := newDBConfig(connectionString, options)
	if err != nil {
		return nil, err
	}
	if err := azbmetrics.StartServer(); err != nil {
		return nil, err
	}

	azureOpts := coreazb.AzureOptions{
		ConnectionString: connectionString,
		Container:        cfg.container,
		Prefix:           cfg.prefix,
		ReadOnly:         cfg.readOnly,
		EntriesPerNode:   cfg.entriesPerNode,
		NodeCacheEntries: cfg.nodeCacheEntries,
	}
	if err := coreazb.EnsureAzurePrefix(ctx, azureOpts); err != nil {
		return nil, err
	}

	db, err := sql.Open("sqlite3", cfg.sqliteDSN)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	if err := db.PingContext(ctx); err != nil {
		db.Close()
		return nil, err
	}
	if err := configureAZBConn(ctx, db, connectionString, cfg); err != nil {
		db.Close()
		return nil, err
	}
	return db, nil
}

func NewReadOnlyDB(ctx context.Context, connectionString string, options ...DBOption) (*sql.DB, error) {
	readOnlyOptions := append(append([]DBOption{}, options...), WithReadOnly(true))
	return NewDB(ctx, connectionString, readOnlyOptions...)
}

func CreateTable(ctx context.Context, db *sql.DB, name, columns string) error {
	_, err := db.ExecContext(ctx, fmt.Sprintf(
		"CREATE VIRTUAL TABLE IF NOT EXISTS %s USING azb (columns=%s)",
		quoteIdentifier(name), quoteSQLString(columns),
	))
	return err
}

func Refresh(ctx context.Context, db *sql.DB, table string) error {
	_, err := db.ExecContext(ctx, "SELECT azb_refresh(?)", table)
	return err
}

func newDBConfig(connectionString string, options []DBOption) (dbConfig, error) {
	parsed, err := coreazb.AzureOptionsFromConnectionString(connectionString)
	if err != nil {
		return dbConfig{}, err
	}
	cfg := dbConfig{
		container: parsed.Container,
		prefix:    coreazb.DefaultAzureBlobPrefix,
		readOnly:  parsed.ReadOnly,
	}
	if parsed.Prefix != "" {
		cfg.prefix = parsed.Prefix
	}
	for _, option := range options {
		option(&cfg)
	}
	if cfg.container == "" {
		return dbConfig{}, fmt.Errorf("azure connection string must include ContainerName or NewDB must use WithContainer")
	}
	if cfg.prefix == "" {
		cfg.prefix = coreazb.DefaultAzureBlobPrefix
	}
	if cfg.sqliteDSN == "" {
		cfg.sqliteDSN = fmt.Sprintf("file:azb-%d?mode=memory&cache=shared", memoryDBID.Add(1))
	}
	return cfg, nil
}

func configureAZBConn(ctx context.Context, db *sql.DB, connectionString string, cfg dbConfig) error {
	_, err := db.ExecContext(ctx, `UPDATE azb_conn SET
		azure_connection_string=?,
		azure_container=?,
		azure_prefix=?,
		readonly=?,
		entries_per_node=?,
		node_cache_entries=?`,
		connectionString,
		cfg.container,
		cfg.prefix,
		strconv.FormatBool(cfg.readOnly),
		optionalInt(cfg.entriesPerNode),
		optionalInt(cfg.nodeCacheEntries),
	)
	return err
}

func optionalInt(value int) interface{} {
	if value == 0 {
		return nil
	}
	return strconv.Itoa(value)
}

func quoteIdentifier(identifier string) string {
	return `"` + strings.ReplaceAll(identifier, `"`, `""`) + `"`
}

func quoteSQLString(value string) string {
	return `'` + strings.ReplaceAll(value, `'`, `''`) + `'`
}
