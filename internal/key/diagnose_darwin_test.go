//go:build darwin

package key

import (
	"strings"
	"testing"
)

func TestBuildPermissionHintRuntime(t *testing.T) {
	out := `Identifier=com.tencent.xinWeChat
CodeDirectory v=20500 size=19617 flags=0x10000(runtime)
TeamIdentifier=5A4RE8SF68`
	hint := buildPermissionHint(out)
	if !strings.Contains(hint, "Hardened Runtime") {
		t.Fatalf("hint = %q", hint)
	}
	if !strings.Contains(hint, "wxview init --key-source lldb") {
		t.Fatalf("hint missing temporary-copy command: %q", hint)
	}
}

func TestBuildPermissionHintAdHoc(t *testing.T) {
	out := `Identifier=com.tencent.xinWeChat
CodeDirectory v=20500 size=19617 flags=0x2(adhoc)
Signature=adhoc`
	hint := buildPermissionHint(out)
	if !strings.Contains(hint, "ad-hoc signed") {
		t.Fatalf("hint = %q", hint)
	}
}
