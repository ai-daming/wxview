package daemon

import (
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"wxview/internal/key"
)

func TestDiscoveryCoalescesSuccessFailureAndSwitch(t *testing.T) {
	now := time.Unix(100, 0)
	calls := 0
	account := "first"
	fail := false
	d := discoverySnapshot{now: func() time.Time { return now }, discover: func() (key.TargetDB, error) {
		calls++
		if fail {
			return key.TargetDB{}, fmt.Errorf("synthetic discovery failure")
		}
		return key.TargetDB{Account: account, DataDir: t.TempDir()}, nil
	}}
	burst := func() {
		var wg sync.WaitGroup
		for i := 0; i < 50; i++ {
			wg.Add(1)
			go func() { defer wg.Done(); _, _, _ = d.get() }()
		}
		wg.Wait()
	}
	burst()
	if calls != 1 {
		t.Fatalf("got %d probes for initial burst", calls)
	}
	now = now.Add(19 * time.Second)
	burst()
	if calls != 1 {
		t.Fatal("early account probe")
	}
	fail = true
	now = now.Add(time.Second)
	burst()
	if calls != 2 {
		t.Fatal("failed probe was not coalesced")
	}
	if _, _, err := d.get(); err == nil {
		t.Fatal("failure silently reused account")
	}
	fail = false
	account = "second"
	now = now.Add(20 * time.Second)
	burst()
	got, _, err := d.get()
	if err != nil || got.Account != "second" || calls != 3 {
		t.Fatalf("switch: %v %v calls=%d", got, err, calls)
	}
}

func TestDiscoveryOneMinuteSixWatchers(t *testing.T) {
	now := time.Unix(100, 0)
	dir := t.TempDir()
	d := discoverySnapshot{now: func() time.Time { return now }, discover: func() (key.TargetDB, error) { return key.TargetDB{Account: "synthetic", DataDir: dir}, nil }}
	for second := 0; second < 60; second++ {
		for watcher := 0; watcher < 6; watcher++ {
			if _, _, err := d.get(); err != nil {
				t.Fatal(err)
			}
		}
		now = now.Add(time.Second)
	}
	stats := d.statistics()
	if stats.AccountProbes != 3 || stats.DirectoryScans != 12 {
		t.Fatalf("unexpected one-minute schedule: %+v", stats)
	}
}

func TestDiscoveryFindsNewShardsWithoutAccountProbe(t *testing.T) {
	now := time.Unix(100, 0)
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "message"), 0700); err != nil {
		t.Fatal(err)
	}
	write := func(name string) {
		if err := os.WriteFile(filepath.Join(dir, "message", name), nil, 0600); err != nil {
			t.Fatal(err)
		}
	}
	write("message_0.db")
	d := discoverySnapshot{now: func() time.Time { return now }, discover: func() (key.TargetDB, error) { return key.TargetDB{Account: "synthetic", DataDir: dir}, nil }}
	_, paths, err := d.get()
	if err != nil || len(paths) != 1 {
		t.Fatalf("initial paths: %v %v", paths, err)
	}
	write("message_1.db")
	now = now.Add(4 * time.Second)
	_, paths, _ = d.get()
	if len(paths) != 1 {
		t.Fatal("directory check ran too early")
	}
	now = now.Add(time.Second)
	_, paths, err = d.get()
	if err != nil || len(paths) != 2 {
		t.Fatalf("new shard missing: %v %v", paths, err)
	}
	if d.statistics().AccountProbes != 1 {
		t.Fatal("new shard triggered process probe")
	}
}

func TestDiscoveryStatisticsDoNotWaitForProbe(t *testing.T) {
	started := make(chan struct{})
	release := make(chan struct{})
	done := make(chan struct{})
	d := discoverySnapshot{discover: func() (key.TargetDB, error) {
		close(started)
		<-release
		return key.TargetDB{}, fmt.Errorf("synthetic timeout")
	}}
	go func() { defer close(done); _, _, _ = d.get() }()
	<-started
	statsDone := make(chan struct{})
	go func() { _ = d.statistics(); close(statsDone) }()
	select {
	case <-statsDone:
	case <-time.After(time.Second):
		close(release)
		<-done
		t.Fatal("health statistics blocked behind probe")
	}
	close(release)
	<-done
}
