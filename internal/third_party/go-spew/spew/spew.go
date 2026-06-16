package spew

import "fmt"

type ConfigState struct {
	Indent                  string
	DisablePointerAddresses bool
	DisableCapacities       bool
	SortKeys                bool
	DisableMethods          bool
	MaxDepth                int
}

func (c ConfigState) Sdump(values ...interface{}) string {
	if len(values) == 1 {
		return fmt.Sprintf("%#v\n", values[0])
	}
	return fmt.Sprintf("%#v\n", values)
}
