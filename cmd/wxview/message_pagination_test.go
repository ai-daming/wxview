package main

import (
	"testing"
	wxquery "wxview/internal/query"
)

func TestMessageCursorArgsFreezeBounds(t *testing.T) {
	args := buildMessagesCursorArgs("synthetic", wxquery.MessagesResult{Order: "desc", NextCursor: "opaque", Range: wxquery.TimeRange{Start: 10, End: 20, HasStart: true, HasEnd: true}}, 100, true, true)
	found := map[string]string{}
	for i := 1; i < len(args); i++ {
		if args[i] == "--source" || args[i] == "--no-index" {
			found[args[i]] = "true"
			continue
		}
		if i+1 >= len(args) {
			t.Fatal("missing argument value")
		}
		found[args[i]] = args[i+1]
		i++
	}
	for key, want := range map[string]string{"--username": "synthetic", "--order": "desc", "--cursor": "opaque", "--start": "10", "--end": "20", "--limit": "100", "--format": "json", "--source": "true", "--no-index": "true"} {
		if found[key] != want {
			t.Fatalf("%s=%s want %s", key, found[key], want)
		}
	}
	if len(found) != 9 {
		t.Fatalf("unexpected flags: %v", found)
	}
}
