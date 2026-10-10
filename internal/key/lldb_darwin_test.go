//go:build darwin

package key

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestSelectCaptureAccountDoesNotGuess(t *testing.T) {
	home, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	base := filepath.Join(home, "Library/Containers/com.tencent.xinWeChat/Data/Documents/xwechat_files")
	a := filepath.Join(base, "first", "db_storage")
	captureFixture(t, a, "contact/contact.db", 1)
	if got, err := selectCaptureRoot(home, ""); err != nil || got != a {
		t.Fatalf("single account: %s %v", got, err)
	}
	b := filepath.Join(base, "second", "db_storage")
	captureFixture(t, b, "contact/contact.db", 2)
	if _, err := selectCaptureRoot(home, ""); err == nil {
		t.Fatal("multiple accounts guessed")
	}
	if got, err := selectCaptureRoot(home, b); err != nil || got != b {
		t.Fatalf("explicit account: %s %v", got, err)
	}
	outside := captureRoot(t)
	captureFixture(t, outside, "contact/contact.db", 3)
	for _, bad := range []string{"relative", outside, base} {
		if _, err := selectCaptureRoot(home, bad); err == nil {
			t.Fatal("invalid root accepted")
		}
	}
	link := filepath.Join(base, "alias")
	if err := os.Symlink(filepath.Dir(a), link); err != nil {
		t.Fatal(err)
	}
	if _, err := selectCaptureRoot(home, filepath.Join(link, "db_storage")); err == nil {
		t.Fatal("symlink accepted")
	}
}

// A fake helper exercises the parent protocol without launching or touching WeChat.
func TestCaptureProcessCompletionAndPrivacy(t *testing.T) {
	for _, tc := range []struct {
		name, reason string
		closed       bool
		code         int
		success      bool
	}{
		{"complete", "complete", true, 0, true},
		{"deadline", "timeout", true, 0, true},
		{"crash", "process_ended", true, 2, false},
		{"permission", "permission_denied", true, 2, false},
		{"unclosed", "complete", false, 0, false},
		{"bad exit", "complete", true, 2, false},
		{"cancelled", "cancelled", true, 2, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			closed := "false"
			if tc.closed {
				closed = "true"
			}
			script := `import sys
print('private raw output that must not escape')
print('{"type":"progress","captured":1,"total":2,"secret":"hidden"}')
print('{"type":"finished","reason":"` + tc.reason + `","closed":` + closed + `}')
sys.exit(` + string(rune('0'+tc.code)) + `)`
			var out bytes.Buffer
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			final, err := runCaptureProcess(ctx, exec.Command("/usr/bin/python3", "-c", script), &out)
			if (err == nil) != tc.success || final.Reason != tc.reason {
				t.Fatalf("reason=%s err=%v", final.Reason, err)
			}
			if strings.Contains(out.String(), "private") || strings.Contains(out.String(), "hidden") {
				t.Fatal("helper leaked")
			}
		})
	}
}

func TestCaptureProcessCancellationClosesChild(t *testing.T) {
	marker := filepath.Join(t.TempDir(), "closed")
	// Run a disposable stand-in child; the helper owns and kills it in finally.
	script := `import subprocess,time,sys
p=subprocess.Popen(['/bin/sleep','30'])
try:
 print('{"type":"launched"}',flush=True)
 while True: time.sleep(.1)
except KeyboardInterrupt:
 pass
finally:
 p.terminate(); p.wait()
 open(sys.argv[1],'w').write('closed')
 print('{"type":"finished","reason":"cancelled","closed":true}',flush=True)
`
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	ready := make(chan struct{}, 1)
	out := &launchCancelWriter{ready: ready}
	go func() {
		select {
		case <-ready:
			cancel()
		case <-ctx.Done():
		}
	}()
	// The deadline also bounds a broken test helper.
	bounded, stop := context.WithTimeout(ctx, 10*time.Second)
	defer stop()
	_, err := runCaptureProcess(bounded, exec.Command("/usr/bin/python3", "-c", script, marker), out)
	if err == nil {
		t.Fatal("cancelled capture accepted")
	}
	if _, err := os.Stat(marker); err != nil {
		t.Fatal("child cleanup did not finish")
	}
}

type launchCancelWriter struct{ ready chan struct{} }

func (w *launchCancelWriter) Write(p []byte) (int, error) {
	select {
	case w.ready <- struct{}{}:
	default:
	}
	return len(p), nil
}
