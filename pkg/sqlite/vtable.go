package mod

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"go.riyazali.net/sqlite"

	"github.com/Hoverhuang-er/azbdb/internal/writetime"
	"github.com/Hoverhuang-er/azbdb/pkg/azb"
)

type AZBConn struct {
	mu               sync.Mutex
	tables           map[string]*azb.VirtualTable
	defaultAzureOpts azb.AzureOptions
	ctx              context.Context
	ctxCancel        func()
	deadline         time.Time
	writeTime        time.Time
	txFixedWriteTime bool
}

func (sc *AZBConn) ResetContext() {
	if sc.ctxCancel != nil {
		sc.ctxCancel()
		sc.ctxCancel = nil
	}
	sc.ctx = context.Background()
	if !sc.deadline.IsZero() {
		sc.ctx, sc.ctxCancel = context.WithDeadline(sc.ctx, sc.deadline)
	}
	if !sc.writeTime.IsZero() {
		sc.ctx = writetime.NewContext(sc.ctx, sc.writeTime)
	}
}

func (sc *AZBConn) addTable(table *azb.VirtualTable) error {
	sc.mu.Lock()
	defer sc.mu.Unlock()
	if _, ok := sc.tables[table.Name]; ok {
		return fmt.Errorf("table already exists: %s", table.Name)
	}
	sc.tables[table.Name] = table
	return nil
}

func (sc *AZBConn) getTable(name string) *azb.VirtualTable {
	sc.mu.Lock()
	defer sc.mu.Unlock()
	return sc.tables[name]
}

func (sc *AZBConn) removeTable(table *azb.VirtualTable) {
	sc.mu.Lock()
	defer sc.mu.Unlock()
	if sc.tables[table.Name] == table {
		delete(sc.tables, table.Name)
	}
}

type Module struct {
	sc *AZBConn
}

func (c *Module) Connect(conn *sqlite.Conn, args []string,
	declare func(string) error) (sqlite.VirtualTable, error) {

	args = args[2:]
	table, err := azb.NewWithOptions(c.sc.ctx, args, c.sc.defaultAzureOpts)
	if err != nil {
		return nil, err
	}

	err = declare(table.SchemaString)
	if err != nil {
		table.Disconnect()
		return nil, fmt.Errorf("declare: %w", err)
	}
	if err := c.sc.addTable(table); err != nil {
		table.Disconnect()
		return nil, err
	}

	vt := &VirtualTable{
		module: c,
		common: table,
	}

	return vt, nil
}

func (c *Module) Create(conn *sqlite.Conn, args []string, declare func(string) error) (sqlite.VirtualTable, error) {
	return c.Connect(conn, args, declare)
}

type VirtualTable struct {
	common *azb.VirtualTable
	module *Module
}

var _ sqlite.TwoPhaseCommitter = (*VirtualTable)(nil)

func mapOp(in sqlite.ConstraintOp, usable bool) azb.Op {
	if !usable {
		return azb.OpIgnore
	}
	switch in {
	case sqlite.INDEX_CONSTRAINT_EQ:
		return azb.OpEQ
	case sqlite.INDEX_CONSTRAINT_GT:
		return azb.OpGT
	case sqlite.INDEX_CONSTRAINT_GE:
		return azb.OpGE
	case sqlite.INDEX_CONSTRAINT_LT:
		return azb.OpLT
	case sqlite.INDEX_CONSTRAINT_LE:
		return azb.OpLE
	}
	return azb.OpIgnore
}

func (c *VirtualTable) BestIndex(input *sqlite.IndexInfoInput) (*sqlite.IndexInfoOutput, error) {
	indexIn := make([]azb.IndexInput, len(input.Constraints))
	for i, c := range input.Constraints {
		op := mapOp(c.Op, c.Usable)
		indexIn[i] = azb.IndexInput{
			ColumnIndex: c.ColumnIndex,
			Op:          op,
		}
	}
	orderIn := make([]azb.OrderInput, len(input.OrderBy))
	for i, o := range input.OrderBy {
		orderIn[i] = azb.OrderInput{
			Column: o.ColumnIndex,
			Desc:   o.Desc,
		}
	}
	indexOut, err := c.common.BestIndex(indexIn, orderIn)
	if err != nil {
		return nil, toSqlite(err)
	}
	used := make([]*sqlite.ConstraintUsage, len(indexIn))
	for i := range indexOut.Used {
		if indexOut.Used[i] {
			used[i] = &sqlite.ConstraintUsage{
				ArgvIndex: i + 1,
				//Omit: true, // no known cases where this doesn't work, but...
			}
		}
	}
	return &sqlite.IndexInfoOutput{
		EstimatedCost:   indexOut.EstimatedCost,
		ConstraintUsage: used,
		OrderByConsumed: indexOut.AlreadyOrdered,
		IndexNumber:     indexOut.IdxNum,
		IndexString:     indexOut.IdxStr,
	}, nil
}

func (c *VirtualTable) Open() (sqlite.VirtualCursor, error) {
	common, err := c.common.Open()
	if err != nil {
		return nil, toSqlite(err)
	}
	return &Cursor{
		common: common,
		ctx:    c.module.sc.ctx,
	}, nil
}

func (c *VirtualTable) Disconnect() error {
	if err := toSqlite(c.common.Disconnect()); err != nil {
		return err
	}
	c.module.sc.removeTable(c.common)
	if c.module.sc.ctxCancel != nil {
		c.module.sc.ctxCancel()
		c.module.sc.ctxCancel = nil
	}

	return nil
}

func (c *VirtualTable) Destroy() error {
	return c.Disconnect()
}

type Cursor struct {
	common *azb.Cursor
	ctx    context.Context
}

func (c *Cursor) Next() error {
	return toSqlite(c.common.Next(c.ctx))
}

func (c *Cursor) Column(ctx *sqlite.VirtualTableContext, i int) error {
	v, err := c.common.Column(i)
	if err != nil {
		return toSqlite(err)
	}
	setContextResult(ctx, v, i)
	return nil
}

func setContextResult(ctx *sqlite.VirtualTableContext, v interface{}, colIndex int) {
	switch x := v.(type) {
	case nil:
		ctx.ResultNull()
	case []byte:
		ctx.ResultBlob(x)
	case float64:
		ctx.ResultFloat(x)
	case int:
		ctx.ResultInt(x)
	case int64:
		ctx.ResultInt64(x)
	case string:
		ctx.ResultText(x)
	default:
		ctx.ResultError(fmt.Errorf("column %d: cannot convert %T", colIndex, x))
	}
}

func (c *Cursor) Filter(_ int, idxStr string, values ...sqlite.Value) error {
	es := make([]interface{}, len(values))
	for i := range values {
		es[i] = valueToGo(values[i])
	}
	return toSqlite(c.common.Filter(c.ctx, idxStr, es))
}
func (c *Cursor) Rowid() (int64, error) {
	i, err := c.common.Rowid()
	return i, toSqlite(err)
}
func (c *Cursor) Eof() bool    { return c.common.Eof() }
func (c *Cursor) Close() error { return toSqlite(c.common.Close()) }

func init() {
	sqlite.Register(func(api *sqlite.ExtensionApi) (sqlite.ErrorCode, error) {
		sc := &AZBConn{
			ctx:    context.Background(),
			tables: make(map[string]*azb.VirtualTable),
		}
		err := api.CreateModule("azb", &Module{sc},
			sqlite.ReadOnly(false), sqlite.Transaction(true),
			sqlite.TwoPhaseCommit(true))
		if err != nil {
			return sqlite.SQLITE_ERROR, err
		}
		err = api.CreateModule("azb_changes", &ChangesModule{sc})
		if err != nil {
			return sqlite.SQLITE_ERROR, err
		}
		err = api.CreateModule("azb_conn", &ConnModule{sc},
			sqlite.ReadOnly(false),
			sqlite.EponymousOnly(true))
		if err != nil {
			return sqlite.SQLITE_ERROR, err
		}
		if err := api.CreateFunction("azb_refresh", &RefreshFunc{sc}); err != nil {
			return sqlite.SQLITE_ERROR, fmt.Errorf("azb_refresh: %w", err)
		}
		err = api.CreateModule("azb_vacuum", &VacuumModule{sc}, sqlite.EponymousOnly(true))
		if err != nil {
			return sqlite.SQLITE_ERROR, fmt.Errorf("azb_vacuum: %w", err)
		}
		if err := api.CreateFunction("azb_version", &VersionFunc{sc}); err != nil {
			return sqlite.SQLITE_ERROR, fmt.Errorf("azb_version: %w", err)
		}
		return sqlite.SQLITE_OK, nil
	})
}

func valuesToGo(values []sqlite.Value) map[int]interface{} {
	res := make(map[int]interface{}, len(values))
	for i := range values {
		if values[i].NoChange() {
			continue
		}
		res[i] = valueToGo(values[i])
	}
	return res
}
func valueToGo(value sqlite.Value) interface{} {
	switch value.Type() {
	case sqlite.SQLITE_BLOB:
		return value.Blob()
	case sqlite.SQLITE_FLOAT:
		return value.Float()
	case sqlite.SQLITE_INTEGER:
		return value.Int64()
	case sqlite.SQLITE_NULL:
		return nil
	case sqlite.SQLITE_TEXT:
		return value.Text()
	default:
		panic(fmt.Sprintf("cannot convert type %d", value.Type()))
	}
}

func (c *VirtualTable) Insert(values ...sqlite.Value) (int64, error) {
	i, err := c.common.Insert(c.module.sc.ctx, valuesToGo(values))
	return i, toSqlite(err)
}

func toSqlite(err error) error {
	switch err {
	case azb.ErrAZBConstraintNotNull:
		return sqlite.SQLITE_CONSTRAINT_NOTNULL
	case azb.ErrAZBConstraintPrimaryKey:
		return sqlite.SQLITE_CONSTRAINT_PRIMARYKEY
	case azb.ErrAZBConstraintUnique:
		return sqlite.SQLITE_CONSTRAINT_UNIQUE
	default:
		return err
	}
}

func (c *VirtualTable) Update(value sqlite.Value, values ...sqlite.Value) error {
	return toSqlite(c.common.Update(c.module.sc.ctx, valueToGo(value), valuesToGo(values)))
}

func (c *VirtualTable) Replace(oldValue, newValue sqlite.Value, values ...sqlite.Value) error {
	return errors.New("unimplemented")
}

func (c *VirtualTable) Delete(value sqlite.Value) error {
	return toSqlite(c.common.Delete(c.module.sc.ctx, valueToGo(value)))
}

func (c *VirtualTable) Begin() error {
	if c.module.sc.writeTime.IsZero() {
		c.module.sc.writeTime = time.Now()
		c.module.sc.txFixedWriteTime = true
		c.module.sc.ResetContext()
	}
	return toSqlite(c.common.Begin(c.module.sc.ctx))
}

func (c *VirtualTable) Commit() error {
	if c.module.sc.txFixedWriteTime {
		c.module.sc.writeTime = time.Time{}
		c.module.sc.txFixedWriteTime = false
		c.module.sc.ResetContext()
	}
	return nil
}

func (c *VirtualTable) Sync() error {
	if c.common.AzureOptions.ReadOnly {
		return nil
	}

	return toSqlite(c.common.Commit(c.module.sc.ctx))
}

func (c *VirtualTable) Rollback() error {
	res := toSqlite(c.common.Rollback())
	if c.module.sc.txFixedWriteTime {
		c.module.sc.writeTime = time.Time{}
		c.module.sc.txFixedWriteTime = false
		c.module.sc.ResetContext()
	}
	return res
}
