AZB
===

AZB is an Azure Blob-backed SQLite WAL library for Go applications and SQLite extension users.
It stores virtual-table row versions in Azure Blob Storage under a WAL-style blob prefix, so writers can commit versions while read-only handles open the same prefix for query traffic.

Use it when one Azure Storage connection string should provide:

* SQLite-compatible reads and writes through `database/sql`.
* Separate writer and read-only handles.
* Shared durable state across multiple Go processes or containers.
* Azure Blob Storage as the only persistence backend.

Quick Start for Go
==================

Add `ContainerName=<container>` to a standard Azure Storage connection string. `Prefix=<prefix>` is optional and defaults to `sqlite_wal`.

```
package main

import (
        "context"

        azb "github.com/Hoverhuang-er/azbdb"
)

func open(ctx context.Context) error {
        db, err := azb.NewDB(ctx, "DefaultEndpointsProtocol=https;AccountName=acct;AccountKey=key;EndpointSuffix=core.windows.net;ContainerName=mycontainer")
        if err != nil {
                return err
        }
        defer db.Close()

        if err := azb.CreateTable(ctx, db, "users", "id primary key, name, email"); err != nil {
                return err
        }
        _, err = db.ExecContext(ctx, "insert into users values(?,?,?)", 1, "jeff", "jeff@example.org")
        return err
}
```

`NewDB` creates the Azure Blob prefix marker and stores each table under `sqlite_wal/<table>/azb-rows/` by default.

Read/Write Separation
=====================

Use `NewDB` for writers and `NewReadOnlyDB` for read handles. Both handles use the same connection string, container, and prefix.

```
writer, err := azb.NewDB(ctx, connectionString)
if err != nil {
        return err
}
defer writer.Close()

reader, err := azb.NewReadOnlyDB(ctx, connectionString)
if err != nil {
        return err
}
defer reader.Close()

if err := azb.CreateTable(ctx, writer, "users", "id primary key, name, email"); err != nil {
        return err
}
if err := azb.CreateTable(ctx, reader, "users", "id primary key, name, email"); err != nil {
        return err
}

_, err = writer.ExecContext(ctx, "insert into users values(?,?,?)", 1, "jeff", "jeff@example.org")
if err != nil {
        return err
}
if err := azb.Refresh(ctx, reader, "users"); err != nil {
        return err
}
```

A read-only handle rejects writes. Writers commit new versions. Readers reopen the table state with `azb.Refresh` or the SQL function `azb_refresh` to see versions committed by other handles.

Options
=======

`NewDB` and `NewReadOnlyDB` accept these options:

* `azb.WithContainer(container)` overrides `ContainerName` from the connection string.
* `azb.WithPrefix(prefix)` overrides the blob prefix. Default: `sqlite_wal`.
* `azb.ReadOnly()` or `azb.WithReadOnly(true)` opens a read-only handle.
* `azb.WithSQLiteDSN(dsn)` controls the local SQLite DSN. Default: an in-memory SQLite database.
* `azb.WithEntriesPerNode(entries)` tunes row packing per Azure Blob object.
* `azb.WithNodeCacheEntries(entries)` tunes in-memory node caching.

Prometheus Metrics
==================

AZB starts a goroutine-backed Prometheus endpoint on `:9190/metrics` when an Azure Blob WAL client is opened. The endpoint only exports AZB SQLite WAL metrics; it does not expose metrics from the host Go application.

Exported metrics:

* `azb_sqlite_wal_read_operations_total`
* `azb_sqlite_wal_write_operations_total`
* `azb_sqlite_wal_bytes_total`
* `azb_sqlite_wal_operation_seconds_total`
* `azb_sqlite_wal_operations_in_flight`

Operation labels distinguish `load`, `list`, `store`, and `delete`. Result labels distinguish successful and failed read/write operations.

Examples
========

* `fiber-azbdb-dynamic-page` - Fiber dynamic guestbook using AZBDB as its database dependency. See [examples/fiber-azbdb-dynamic-page/Readme.md](examples/fiber-azbdb-dynamic-page/Readme.md).
* `agentmesh-azbdb-agent` - AgentMesh-compatible Go MCP agent with durable AZBDB memory. See [examples/agentmesh-azbdb-agent/Readme.md](examples/agentmesh-azbdb-agent/Readme.md).

SQLite Extension Usage
======================

Build the extension, load `azb`, then create a virtual table with `USING azb`.

```
sqlite> .open mydb.sqlite
sqlite> .load ./azb
sqlite> create virtual table users using azb (
   ...> columns='id primary key, name, email',
   ...> azure_connection_string='DefaultEndpointsProtocol=https;AccountName=acct;AccountKey=key;EndpointSuffix=core.windows.net',
   ...> azure_container='mycontainer',
   ...> azure_prefix='sqlite_wal/users');
sqlite> insert into users values (1, 'jeff', 'jeff@example.org');
sqlite> select * from users;
```

Virtual table arguments:

* `columns='<colname> [primary key], ...'` defines the SQLite columns and primary key.
* `azure_container='mycontainer'` selects the Azure Blob container.
* `azure_connection_string='...'` supplies the Azure Storage connection string.
* `azure_prefix='prefix'` selects the blob prefix.
* `readonly` opens the table without write permission.
* `entries_per_node=<N>` changes row packing. Default: 4096.
* `node_cache_entries=<N>` caches nodes in memory. Default: 0.
* `azure_account`, `azure_account_key`, and `azure_endpoint` remain available for extension users that do not use a full connection string.

Version Tracking
================

Each transaction may commit a new version. Multiple writers can commit from the same previous version; the next reader merges visible versions. Conflicts resolve with last-write-wins per column. Deletes hide later updates for the same row until another insert for that key.

```
sqlite> select azb_version('users');
["version-before"]
sqlite> insert into users values (2, 'joe', 'joe@example.org');
sqlite> update users set email='new_address@example.org' where id=1;
sqlite> select azb_version('users');
["version-after"]
sqlite> create virtual table additions using azb_changes (table='users', from='["version-before"]', to='["version-after"]');
sqlite> select * from additions;
sqlite> drop table additions;
```

Flip `from` and `to` in `azb_changes` to inspect removed rows.

Connection State
================

`azb_conn` configures per-connection behavior:

```
update azb_conn set deadline=datetime('now','+3 seconds');
update azb_conn set write_time='2026-06-16 12:00:00';
```

`deadline` cancels network operations after the timestamp. `write_time` fixes row modification time for idempotent retries.

Maintenance
===========

Use `azb_vacuum` to remove old versions and tombstoned rows older than a cutoff:

```
select * from azb_vacuum('users', datetime('now','-7 days'));
```

Building from Source
====================

Requires Go >= 1.22.

```
go vet ./...
go generate ./cmd/azb-sharedlib
go test ./...
```

The shared extension is generated as `cmd/azb-sharedlib/azb.so` on Linux or `cmd/azb-sharedlib/azb.dylib` on macOS. The static archive target is `cmd/azb-staticlib/azb.a`.

Reference
=========

* Go package: `github.com/Hoverhuang-er/azbdb`
* SQLite virtual table module: `azb`
* Changes virtual table module: `azb_changes`
* Connection table: `azb_conn`
* Refresh function: `azb_refresh(table)`
* Version function: `azb_version(table)`
* Vacuum table-valued function: `azb_vacuum(table, before_timestamp)`
