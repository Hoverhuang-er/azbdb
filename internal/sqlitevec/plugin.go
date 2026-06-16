package sqlitevec

import "errors"

const ModuleName = "sqlite_vec"

var ErrNotImplemented = errors.New("sqlitevec plugin support is reserved but not implemented")

type Capability struct {
	Enabled    bool
	ModuleName string
}

func ReservedCapability() Capability {
	return Capability{ModuleName: ModuleName}
}

func (c Capability) Validate() error {
	if !c.Enabled {
		return nil
	}
	return ErrNotImplemented
}
