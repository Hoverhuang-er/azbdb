package mast

import (
	"fmt"
	"log/slog"
	"strings"
)

func debugf(format string, args ...interface{}) {
	slog.Debug(fmt.Sprintf(strings.TrimSpace(format), args...))
}
