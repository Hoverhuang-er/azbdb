package sqlitevec

import (
	"errors"
	"testing"
)

func TestReservedCapabilityIsDisabled(t *testing.T) {
	capability := ReservedCapability()
	if capability.ModuleName != ModuleName {
		t.Fatalf("ModuleName = %q, want %q", capability.ModuleName, ModuleName)
	}
	if capability.Enabled {
		t.Fatal("reserved sqlitevec capability must be disabled by default")
	}
	if err := capability.Validate(); err != nil {
		t.Fatalf("disabled capability returned error: %v", err)
	}
}

func TestEnabledCapabilityIsExplicitlyUnimplemented(t *testing.T) {
	capability := ReservedCapability()
	capability.Enabled = true
	if err := capability.Validate(); !errors.Is(err, ErrNotImplemented) {
		t.Fatalf("Validate() = %v, want ErrNotImplemented", err)
	}
}
