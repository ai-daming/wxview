package daemon

import (
	"testing"
	"time"
)

func TestDebouncerCoalescesTriggers(t *testing.T) {
	ch := make(chan struct{}, 3)
	d := NewDebouncer(30*time.Millisecond, func() {
		ch <- struct{}{}
	})
	defer d.Stop()

	d.Trigger()
	d.Trigger()
	d.Trigger()

	select {
	case <-ch:
	case <-time.After(200 * time.Millisecond):
		t.Fatal("expected debounced fire")
	}

	select {
	case <-ch:
		t.Fatal("expected one debounced fire")
	case <-time.After(80 * time.Millisecond):
	}
}

func TestDebouncerLongCallbackCoalescesWithoutOverlap(t *testing.T) {
	started := make(chan struct{}, 10)
	release := make(chan struct{})
	d := NewDebouncer(time.Millisecond, func() { started <- struct{}{}; <-release })
	defer d.Stop()
	d.Trigger()
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("first callback missing")
	}
	for i := 0; i < 100; i++ {
		d.Trigger()
	}
	select {
	case <-started:
		t.Fatal("overlapping callbacks")
	case <-time.After(20 * time.Millisecond):
	}
	release <- struct{}{}
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("pending callback missing")
	}
	d.Stop()
	release <- struct{}{}
	d.Trigger()
	select {
	case <-started:
		t.Fatal("callback after stop")
	case <-time.After(20 * time.Millisecond):
	}
}
