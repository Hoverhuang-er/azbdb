package mod

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"time"

	"go.riyazali.net/sqlite"

	"github.com/Hoverhuang-er/azbdb/pkg/azb"
)

type ConnModule struct {
	sc *AZBConn
}

var _ sqlite.WriteableVirtualTable = (*ConnModule)(nil)

func (c *ConnModule) Connect(conn *sqlite.Conn, args []string,
	declare func(string) error) (sqlite.VirtualTable, error) {

	err := declare(`create table azb_conn(
		deadline,
		write_time,
		azure_connection_string HIDDEN,
		azure_container HIDDEN,
		azure_prefix HIDDEN,
		readonly HIDDEN,
		entries_per_node HIDDEN,
		node_cache_entries HIDDEN
	)`)
	if err != nil {
		return nil, fmt.Errorf("declare: %w", err)
	}

	return c, nil
}

func (vm *ConnModule) BestIndex(input *sqlite.IndexInfoInput) (*sqlite.IndexInfoOutput, error) {
	return &sqlite.IndexInfoOutput{
		ConstraintUsage: make([]*sqlite.ConstraintUsage, len(input.Constraints)),
	}, nil
}

func (vm *ConnModule) Open() (sqlite.VirtualCursor, error) {
	return &ConnCursor{vm, false}, nil
}

func (vm *ConnModule) Disconnect() error { return nil }
func (vm *ConnModule) Destroy() error    { return nil }

type ConnCursor struct {
	vm  *ConnModule
	eof bool
}

func (vc *ConnCursor) Next() error {
	vc.eof = true
	return nil
}

func (vc *ConnCursor) Rowid() (int64, error) {
	return 0, nil
}

func (vc *ConnCursor) Column(context *sqlite.VirtualTableContext, i int) error {
	switch i {
	case 0:
		if vc.vm.sc.deadline.IsZero() {
			context.ResultNull()
		} else {
			context.ResultText(vc.vm.sc.deadline.Format(azb.SQLiteTimeFormat))
		}
	case 1:
		if vc.vm.sc.writeTime.IsZero() {
			context.ResultNull()
		} else {
			context.ResultText(vc.vm.sc.writeTime.Format(azb.SQLiteTimeFormat))
		}
	case 2, 3, 4, 5, 6, 7:
		context.ResultNull()
	default:
		context.ResultError(fmt.Errorf("unhandled column %d", i))
	}
	return nil
}

func (vc *ConnCursor) Eof() bool    { return vc.eof }
func (vc *ConnCursor) Close() error { return nil }

func (vc *ConnCursor) Filter(_ int, idxStr string, values ...sqlite.Value) error {
	return nil
}

func (c *ConnModule) Update(value sqlite.Value, values ...sqlite.Value) error {
	var err error
	if len(values) != 8 {
		return errors.New("wrong number of column values")
	}

	deadline, writeTime := values[0], values[1]
	connectionString, container := values[2], values[3]
	prefix, readonly := values[4], values[5]
	entriesPerNode, nodeCacheEntries := values[6], values[7]
	if !deadline.NoChange() {
		if deadline.IsNil() || deadline.Text() == "" {
			c.sc.deadline = time.Time{}
			c.sc.ctx = context.Background()
		} else {
			c.sc.deadline, err = time.Parse(azb.SQLiteTimeFormat, deadline.Text())
			if err != nil {
				// TODO: fix other time parsing error messages
				return fmt.Errorf("deadline: must be like %s", azb.SQLiteTimeFormat)
			}
		}
	}

	if !writeTime.NoChange() {
		if writeTime.IsNil() || writeTime.Text() == "" {
			c.sc.writeTime = time.Time{}
		} else {
			c.sc.writeTime, err = time.Parse(azb.SQLiteTimeFormat, writeTime.Text())
			if err != nil {
				return fmt.Errorf("write_time: must be like %s", azb.SQLiteTimeFormat)
			}
		}
	}

	if !connectionString.NoChange() {
		if connectionString.IsNil() || connectionString.Text() == "" {
			c.sc.defaultAzureOpts.ConnectionString = ""
		} else {
			parsed, err := azb.AzureOptionsFromConnectionString(connectionString.Text())
			if err != nil {
				return fmt.Errorf("azure_connection_string: %w", err)
			}
			c.sc.defaultAzureOpts.ConnectionString = parsed.ConnectionString
			if parsed.Container != "" {
				c.sc.defaultAzureOpts.Container = parsed.Container
			}
			if parsed.Prefix != "" {
				c.sc.defaultAzureOpts.Prefix = parsed.Prefix
			}
			if parsed.ReadOnly {
				c.sc.defaultAzureOpts.ReadOnly = true
			}
		}
	}
	if !container.NoChange() {
		if container.IsNil() || container.Text() == "" {
			c.sc.defaultAzureOpts.Container = ""
		} else {
			c.sc.defaultAzureOpts.Container = container.Text()
		}
	}
	if !prefix.NoChange() {
		if prefix.IsNil() || prefix.Text() == "" {
			c.sc.defaultAzureOpts.Prefix = ""
		} else {
			c.sc.defaultAzureOpts.Prefix = prefix.Text()
		}
	}
	if !readonly.NoChange() {
		if readonly.IsNil() || readonly.Text() == "" {
			c.sc.defaultAzureOpts.ReadOnly = false
		} else {
			b, err := strconv.ParseBool(readonly.Text())
			if err != nil {
				return fmt.Errorf("readonly: %w", err)
			}
			c.sc.defaultAzureOpts.ReadOnly = b
		}
	}
	if !entriesPerNode.NoChange() {
		if entriesPerNode.IsNil() || entriesPerNode.Text() == "" {
			c.sc.defaultAzureOpts.EntriesPerNode = 0
		} else {
			v, err := strconv.ParseInt(entriesPerNode.Text(), 10, 32)
			if err != nil {
				return fmt.Errorf("entries_per_node: %w", err)
			}
			c.sc.defaultAzureOpts.EntriesPerNode = int(v)
		}
	}
	if !nodeCacheEntries.NoChange() {
		if nodeCacheEntries.IsNil() || nodeCacheEntries.Text() == "" {
			c.sc.defaultAzureOpts.NodeCacheEntries = 0
		} else {
			v, err := strconv.ParseInt(nodeCacheEntries.Text(), 10, 32)
			if err != nil {
				return fmt.Errorf("node_cache_entries: %w", err)
			}
			c.sc.defaultAzureOpts.NodeCacheEntries = int(v)
		}
	}
	c.sc.txFixedWriteTime = false
	c.sc.ResetContext()

	return nil
}

func (c *ConnModule) Insert(_ ...sqlite.Value) (int64, error) {
	return 0, sqlite.SQLITE_CONSTRAINT_VTAB
}

func (c *ConnModule) Replace(old, new sqlite.Value, _ ...sqlite.Value) error {
	return sqlite.SQLITE_CONSTRAINT_VTAB
}

func (c *ConnModule) Delete(sqlite.Value) error {
	return sqlite.SQLITE_CONSTRAINT_VTAB
}
