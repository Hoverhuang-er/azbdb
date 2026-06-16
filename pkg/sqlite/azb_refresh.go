package mod

import (
	"fmt"

	"go.riyazali.net/sqlite"

	"github.com/Hoverhuang-er/azbdb/pkg/azb"
)

type RefreshFunc struct {
	sc *AZBConn
}

func (h *RefreshFunc) Args() int           { return 1 }
func (h *RefreshFunc) Deterministic() bool { return false }
func (h *RefreshFunc) Step(ctx *sqlite.AggregateContext, values ...sqlite.Value) {
	if ctx.Data() == nil {
		ctx.SetData(&RefreshFuncContext{})
	}

	var val = values[0]
	var fCtx = ctx.Data().(*RefreshFuncContext)

	if !val.IsNil() {
		fCtx.tableName = val.Text()
	}
}
func (h *RefreshFunc) Final(ctx *sqlite.AggregateContext) {
	if ctx.Data() == nil {
		return
	}
	var fCtx = ctx.Data().(*RefreshFuncContext)
	if fCtx.tableName == "" {
		ctx.ResultError(fmt.Errorf("missing table name"))
		return
	}
	vt := h.sc.getTable(fCtx.tableName)
	if vt == nil {
		ctx.ResultError(fmt.Errorf("table not found: %s", fCtx.tableName))
		return
	}
	nt, err := azb.OpenKV(h.sc.ctx, vt.AzureOptions, "azb-rows")
	if err != nil {
		ctx.ResultError(fmt.Errorf("open: %w", err))
		return
	}
	vt.Tree = nt
}

type RefreshFuncContext struct {
	tableName string
}
