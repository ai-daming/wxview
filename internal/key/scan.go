package key

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"wxview/internal/decrypt"
)

func ScanContactKey(ctx context.Context, saltHex string, page1 []byte) (string, error) {
	return ScanDBKey(ctx, saltHex, page1, "contact/contact.db")
}

func ScanDBKey(ctx context.Context, saltHex string, page1 []byte, label string) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	ctx, cancel := beginKeyScan(ctx)
	defer cancel()
	pids, err := wechatPIDs(ctx)
	if err != nil {
		return "", err
	}
	if len(pids) == 0 {
		return "", fmt.Errorf("WeChat process is not running")
	}
	var lastErr error
	for _, pid := range pids {
		keyHex, err := scanSQLCipherPragmaKey(ctx, pid, strings.ToLower(saltHex), page1)
		if ctx.Err() != nil {
			return "", ctx.Err()
		}
		if err != nil {
			if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
				return "", err
			}
			lastErr = err
			continue
		}
		if keyHex == "" {
			continue
		}
		if decrypt.ValidateRawHexKey(page1, keyHex) {
			return strings.ToLower(keyHex), nil
		}
		lastErr = fmt.Errorf("candidate key from pid %d failed hmac verification", pid)
	}
	if lastErr != nil {
		return "", fmt.Errorf("%w. Key scan needs WeChat running and permission to read its process memory.%s", lastErr, wechatPermissionHint(ctx))
	}
	return "", fmt.Errorf("no SQLCipher key found for %s salt %s. Key scan needs WeChat running and permission to read its process memory.%s", label, saltHex, wechatPermissionHint(ctx))
}
