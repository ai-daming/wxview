//go:build (!darwin && !windows) || (darwin && !cgo)

package key

import (
	"context"
	"fmt"
)

func scanSQLCipherPragmaKey(_ context.Context, pid int, saltHex string, _ []byte) (string, error) {
	return "", fmt.Errorf("process memory key scanning is only implemented for macOS with cgo enabled or Windows")
}
