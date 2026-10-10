//go:build !darwin

package key

import (
	"context"
	"fmt"
	"io"
)

func CaptureSupportedKeys(ctx context.Context, root string, progress io.Writer) (EnsureKeysResult, error) {
	return EnsureKeysResult{}, fmt.Errorf("LLDB key capture requires native Apple Silicon macOS")
}
