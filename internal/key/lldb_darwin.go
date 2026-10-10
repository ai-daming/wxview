//go:build darwin

package key

import (
	"bufio"
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"wxview/internal/app"
)

//go:embed lldbhelper/capture.py
var captureScript []byte

//go:embed lldbhelper/LICENSE
var captureLicense []byte

//go:embed lldbhelper/NOTICE
var captureNotice []byte

func selectCaptureRoot(home, requested string) (string, error) {
	base := filepath.Join(home, "Library/Containers/com.tencent.xinWeChat/Data/Documents/xwechat_files")
	canonical, err := filepath.EvalSymlinks(base)
	if err != nil {
		return "", fmt.Errorf("cannot locate WeChat accounts: %w", err)
	}
	base = canonical
	if requested == "" {
		entries, err := os.ReadDir(base)
		if err != nil {
			return "", err
		}
		var roots []string
		for _, e := range entries {
			if !e.IsDir() {
				continue
			}
			r := filepath.Join(base, e.Name(), "db_storage")
			if info, err := os.Stat(filepath.Join(r, "contact/contact.db")); err == nil && info.Mode().IsRegular() {
				roots = append(roots, r)
			}
		}
		if len(roots) != 1 {
			return "", fmt.Errorf("found %d accounts; specify --db-root for the intended account", len(roots))
		}
		requested = roots[0]
	}
	if !filepath.IsAbs(requested) {
		return "", fmt.Errorf("--db-root must be an absolute account db_storage path")
	}
	root, err := filepath.EvalSymlinks(requested)
	if err != nil {
		return "", fmt.Errorf("invalid --db-root: %w", err)
	}
	if root != filepath.Clean(requested) || filepath.Base(root) != "db_storage" || filepath.Dir(filepath.Dir(root)) != base {
		return "", fmt.Errorf("--db-root must be a real account db_storage directly under the current user's xwechat_files; symlinks are not accepted")
	}
	return root, nil
}

func captureCommand(ctx context.Context, executable string, args ...string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, 120*time.Second)
	defer cancel()
	data, err := exec.CommandContext(ctx, executable, args...).Output()
	if err != nil {
		return nil, fmt.Errorf("%s failed; check dependency, permissions or application signature", filepath.Base(executable))
	}
	return data, nil
}

func appSignature(ctx context.Context) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, 120*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, "/usr/bin/codesign", "-dvv", wechatAppPath).CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("cannot read original WeChat signature")
	}
	return string(out), nil
}

type captureEvent struct {
	Type     string `json:"type"`
	Reason   string `json:"reason"`
	Closed   bool   `json:"closed"`
	Captured int    `json:"captured"`
	Total    int    `json:"total"`
}

// Only known progress fields reach stdout. Raw helper/application stderr never
// reaches the user. Cancellation gives the helper time to close its LLDB child.
func runCaptureProcess(ctx context.Context, cmd *exec.Cmd, progress io.Writer) (captureEvent, error) {
	var final captureEvent
	pipe, err := cmd.StdoutPipe()
	if err != nil {
		return final, err
	}
	cmd.Stderr = io.Discard
	if err = cmd.Start(); err != nil {
		return final, fmt.Errorf("could not start LLDB helper")
	}
	scanned := make(chan error, 1)
	go func() {
		s := bufio.NewScanner(pipe)
		for s.Scan() {
			var e captureEvent
			if json.Unmarshal(s.Bytes(), &e) != nil {
				continue
			}
			switch e.Type {
			case "launched":
				fmt.Fprintln(progress, "Temporary WeChat launched; enter the selected account and complete phone confirmation if requested.")
			case "progress", "waiting":
				if e.Captured >= 0 && e.Total >= e.Captured {
					fmt.Fprintf(progress, "keys_captured: %d/%d\n", e.Captured, e.Total)
				}
			case "finished":
				final = e
			}
		}
		scanned <- s.Err()
	}()
	done := make(chan error, 1)
	go func() {
		scanErr := <-scanned
		waitErr := cmd.Wait()
		if scanErr != nil {
			done <- scanErr
		} else {
			done <- waitErr
		}
	}()
	select {
	case err = <-done:
	case <-ctx.Done():
		_ = cmd.Process.Signal(os.Interrupt)
		select {
		case <-done:
		case <-time.After(10 * time.Second):
			_ = cmd.Process.Kill()
			<-done
		}
		return final, ctx.Err()
	}
	if err != nil || !final.Closed || final.Type != "finished" || (final.Reason != "complete" && final.Reason != "timeout") {
		return final, fmt.Errorf("LLDB capture did not finish successfully")
	}
	return final, nil
}

func noWeChat(ctx context.Context) error {
	pids, err := wechatPIDs(ctx)
	if err != nil {
		return err
	}
	if len(pids) > 0 {
		return fmt.Errorf("WeChat is running; save drafts and quit normally from its menu before retrying")
	}
	return nil
}

func CaptureSupportedKeys(ctx context.Context, requested string, progress io.Writer) (result EnsureKeysResult, err error) {
	if progress == nil {
		progress = io.Discard
	}
	if runtime.GOARCH != "arm64" {
		return result, fmt.Errorf("LLDB capture requires native Apple Silicon (no Intel/Rosetta)")
	}
	if os.Geteuid() == 0 {
		return result, fmt.Errorf("run LLDB capture as the logged-in user, without sudo")
	}
	if err = noWeChat(ctx); err != nil {
		return result, err
	}
	home, err := app.HomeDir()
	if err != nil {
		return result, err
	}
	root, err := selectCaptureRoot(home, requested)
	if err != nil {
		return result, err
	}
	targets, _, err := discoverCaptureTargets(root)
	if err != nil {
		return result, err
	}
	module, err := captureCommand(ctx, "/usr/bin/lldb", "-P")
	if err != nil {
		return result, fmt.Errorf("Apple Command Line Tools required: run xcode-select --install; %w", err)
	}
	modulePath := strings.TrimSpace(string(module))
	if !filepath.IsAbs(modulePath) {
		return result, fmt.Errorf("invalid Apple LLDB module path")
	}
	env := append(os.Environ(), "PYTHONPATH="+modulePath, "PYTHONNOUSERSITE=1", "PYTHONDONTWRITEBYTECODE=1")
	checkCtx, cancel := context.WithTimeout(ctx, 120*time.Second)
	check := exec.CommandContext(checkCtx, "/usr/bin/python3", "-s", "-c", "import lldb")
	check.Env = env
	checkErr := check.Run()
	cancel()
	if checkErr != nil {
		return result, fmt.Errorf("Apple Python cannot load LLDB; repair/select Apple Command Line Tools")
	}
	signature, err := appSignature(ctx)
	if err != nil {
		return result, err
	}
	if _, err = captureCommand(ctx, "/usr/bin/codesign", "--verify", "--deep", "--strict", wechatAppPath); err != nil {
		return result, err
	}
	base, err := app.EnsureBaseDir()
	if err != nil {
		return result, err
	}
	resolvedBase, err := filepath.EvalSymlinks(base)
	if err != nil || resolvedBase != base {
		return result, fmt.Errorf("symlink wxview state directory rejected")
	}
	work := filepath.Join(base, "bootstrap")
	if err = os.Mkdir(work, 0700); err != nil {
		return result, fmt.Errorf("cannot create private workspace %s; if it is left from an interrupted run, quit its temporary WeChat and remove that directory manually before retrying", work)
	}
	trial := filepath.Join(work, "WeChat-trial.app")
	keepTrial, saved := false, false
	defer func() {
		defer func() {
			if err != nil && saved {
				err = fmt.Errorf("keys were saved, but cleanup failed; do not recapture just to clean up: %w", err)
			}
			if err == nil {
				fmt.Fprintln(progress, "cleanup_completed: true")
			}
		}()
		// These are our private files, never the account's production key store.
		for _, name := range []string{"candidates.json", "candidates.new", "app-stderr.log"} {
			if e := os.Remove(filepath.Join(work, name)); e != nil && !os.IsNotExist(e) {
				err = errors.Join(err, fmt.Errorf("private file cleanup failed under %s", work))
			}
		}
		cleanupCtx, stop := context.WithTimeout(context.Background(), 120*time.Second)
		defer stop()
		if e := noWeChat(cleanupCtx); e != nil {
			err = errors.Join(err, fmt.Errorf("cannot confirm temporary WeChat has exited; quit it normally and remove %s manually", work))
			return
		}
		if keepTrial {
			return
		}
		if _, e := os.Lstat(trial); e == nil {
			if _, e = captureCommand(cleanupCtx, "/System/Library/Frameworks/CoreServices.framework/Frameworks/LaunchServices.framework/Support/lsregister", "-u", trial); e != nil {
				err = errors.Join(err, fmt.Errorf("temporary app unregister failed; remove %s manually", work))
				return
			}
		}
		if e := os.RemoveAll(work); e != nil {
			err = errors.Join(err, fmt.Errorf("temporary workspace cleanup failed: %s", work))
		}
	}()
	for name, data := range map[string][]byte{"capture.py": captureScript, "LICENSE": captureLicense, "NOTICE": captureNotice} {
		if err = os.WriteFile(filepath.Join(work, name), data, 0600); err != nil {
			return result, err
		}
	}
	rels := make([]string, 0, len(targets))
	for _, t := range targets {
		rels = append(rels, t.DBRelPath)
	}
	input, _ := json.Marshal(struct {
		Root    string   `json:"root"`
		Targets []string `json:"targets"`
	}{root, rels})
	if err = os.WriteFile(filepath.Join(work, "targets.json"), input, 0600); err != nil {
		return result, err
	}
	fmt.Fprintf(progress, "account: %s\ndata_dir: %s\nPreparing temporary WeChat (original signature unchanged).\n", targets[0].Account, root)
	fmt.Fprintln(progress, "The temporary app uses your real WeChat data; returning to the original may require QR-code login.")
	if _, err = captureCommand(ctx, "/usr/bin/ditto", wechatAppPath, trial); err != nil {
		return result, err
	}
	if _, err = captureCommand(ctx, "/usr/bin/codesign", "--force", "--deep", "--sign", "-", trial); err != nil {
		return result, err
	}
	if _, err = captureCommand(ctx, "/usr/bin/codesign", "--verify", "--deep", "--strict", trial); err != nil {
		return result, err
	}
	if err = noWeChat(ctx); err != nil {
		return result, err
	}
	runCtx, stop := context.WithTimeout(ctx, 420*time.Second)
	defer stop()
	cmd := exec.Command("/usr/bin/python3", "-s", filepath.Join(work, "capture.py"), "--exe", filepath.Join(trial, "Contents/MacOS/WeChat"), "--targets", filepath.Join(work, "targets.json"), "--out", filepath.Join(work, "candidates.json"))
	cmd.Env = env
	final, runErr := runCaptureProcess(runCtx, cmd, progress)
	if runErr != nil {
		if final.Reason == "permission_denied" {
			keepTrial = true
			return result, fmt.Errorf("temporary WeChat data access denied. In System Settings > Privacy & Security > Files and Folders > WeChat, enable access to WeChat data. If no entry appears, manually open %s to trigger the prompt. Then quit the temporary app, remove %s, and rerun; no keys saved", trial, work)
		}
		switch final.Reason {
		case "launch_failed":
			return result, fmt.Errorf("LLDB could not launch the temporary app; check Command Line Tools and the terminal's Developer Tools permission; no keys saved")
		case "process_ended":
			return result, fmt.Errorf("temporary WeChat exited before capture completed; check login and data-access permissions; no keys saved")
		case "unexpected_stop":
			return result, fmt.Errorf("temporary WeChat stopped outside the key breakpoint; this build may be incompatible; no keys saved")
		}
		return result, runErr
	}
	if err = ctx.Err(); err != nil {
		return result, err
	}
	if err = noWeChat(ctx); err != nil {
		return result, err
	}
	after, err := appSignature(ctx)
	if err != nil {
		return result, err
	}
	if after != signature {
		return result, fmt.Errorf("original WeChat signature changed unexpectedly; no keys saved, inspect original manually")
	}
	file, err := readCaptureFile(filepath.Join(work, "candidates.json"))
	if err != nil {
		return result, err
	}
	storePath, err := KeyStorePath(targets[0].Account)
	if err != nil {
		return result, err
	}
	old, err := LoadStore(storePath)
	if err != nil {
		return result, fmt.Errorf("cannot load existing key store; no keys saved")
	}
	merged, result, err := mergeCapturedKeys(root, old, file)
	if err != nil {
		return result, err
	}
	if err = ctx.Err(); err != nil {
		return result, err
	}
	if err = SaveStore(storePath, merged); err != nil {
		return result, fmt.Errorf("save captured keys: %w", err)
	}
	saved = true
	fmt.Fprintln(progress, "keys_saved: true\ncache_refreshed: false\nOpen the original /Applications/WeChat.app manually when finished.")
	return result, nil
}
