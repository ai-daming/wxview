//go:build darwin

package key

import (
	"context"
	"os/exec"
	"strings"
)

const wechatAppPath = "/Applications/WeChat.app"

func wechatPermissionHint(ctx context.Context) string {
	cmd := exec.CommandContext(ctx, "codesign", "-dv", wechatAppPath)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return " For scan, try sudo from a local GUI terminal with Developer Tools permission. On macOS (Intel support is experimental), quit WeChat and run `wxview init --key-source lldb` without sudo to capture using a temporary copy."
	}
	return buildPermissionHint(string(out))
}

func buildPermissionHint(codesignOutput string) string {
	lines := compactCodesignLines(codesignOutput)
	base := " WeChat signature: " + strings.Join(lines, "; ") + "."
	if strings.Contains(codesignOutput, "runtime") {
		return base + " This is a Hardened Runtime build; sudo alone can still fail with task_for_pid=5. On macOS (Intel support is experimental), quit WeChat normally and run `wxview init --key-source lldb` without sudo; it uses a temporary copy and preserves the original signature."
	}
	if strings.Contains(codesignOutput, "Signature=adhoc") || strings.Contains(codesignOutput, "flags=0x2(adhoc)") {
		return base + " This looks ad-hoc signed; sudo should usually work. Make sure WeChat was restarted after signing and run from the local GUI user session."
	}
	return base + " For scan, try sudo from a local GUI terminal with Developer Tools permission. On macOS (Intel support is experimental), quit WeChat and run `wxview init --key-source lldb` without sudo to capture using a temporary copy."
}

func compactCodesignLines(out string) []string {
	var lines []string
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		if strings.Contains(line, "flags=") ||
			strings.HasPrefix(line, "Signature=") ||
			strings.HasPrefix(line, "TeamIdentifier=") ||
			strings.HasPrefix(line, "Identifier=") {
			lines = append(lines, line)
		}
	}
	if len(lines) == 0 {
		return []string{"unknown"}
	}
	return lines
}
