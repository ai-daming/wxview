package key

import (
	"context"
	"errors"
	"runtime"
	"time"
)

type windowsScanSessionContextKey struct{}
type windowsScanResult struct {
	material windowsKeyMaterial
	err      error
}
type windowsScanSession struct {
	ctx       context.Context
	budget    memoryReadBudget
	processes map[int]windowsScanResult
}

// init visits databases sequentially. Scope this cache and its total read budget
// to that invocation, never to a package global or a query process.
func newWindowsScanSession(ctx context.Context) *windowsScanSession {
	return &windowsScanSession{ctx: ctx, budget: memoryReadBudget{remaining: 6 << 30}, processes: make(map[int]windowsScanResult)}
}

func beginKeyScan(ctx context.Context) (context.Context, context.CancelFunc) {
	if runtime.GOOS != "windows" {
		return ctx, func() {}
	}
	if _, ok := ctx.Value(windowsScanSessionContextKey{}).(*windowsScanSession); ok {
		return ctx, func() {}
	}
	scanCtx, cancel := context.WithTimeout(ctx, 90*time.Second)
	return context.WithValue(ctx, windowsScanSessionContextKey{}, newWindowsScanSession(scanCtx)), cancel
}

func (s *windowsScanSession) key(ctx context.Context, pid int, salt string, page []byte, collect func(context.Context, *memoryReadBudget) (windowsKeyMaterial, error)) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	result, ok := s.processes[pid]
	if !ok {
		if err := s.ctx.Err(); err != nil {
			return "", err
		}
		result.material, result.err = collect(s.ctx, &s.budget)
		s.processes[pid] = result
	}
	key, err := result.material.key(ctx, salt, page)
	if key != "" || errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return key, err
	}
	return "", errors.Join(result.err, err)
}
