package daemon

import (
	"sync"
	"time"
)

type Debouncer struct {
	delay      time.Duration
	fire       func()
	mu         sync.Mutex
	timer      *time.Timer
	running    bool
	pending    bool
	stopped    bool
	generation uint64
}

func NewDebouncer(delay time.Duration, fire func()) *Debouncer {
	return &Debouncer{delay: delay, fire: fire}
}

func (d *Debouncer) Trigger() {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.stopped {
		return
	}
	if d.running {
		d.pending = true
		return
	}
	d.scheduleLocked()
}

func (d *Debouncer) scheduleLocked() {
	if d.timer != nil {
		d.timer.Stop()
	}
	d.generation++
	generation := d.generation
	d.timer = time.AfterFunc(d.delay, func() { d.run(generation) })
}

func (d *Debouncer) run(generation uint64) {
	d.mu.Lock()
	if d.stopped || generation != d.generation {
		d.mu.Unlock()
		return
	}
	d.timer = nil
	d.running = true
	d.mu.Unlock()
	d.fire()
	d.mu.Lock()
	d.running = false
	if d.pending && !d.stopped {
		d.pending = false
		d.scheduleLocked()
	}
	d.mu.Unlock()
}

// Stop cancels queued work; an already-running callback finishes normally.
func (d *Debouncer) Stop() {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.stopped = true
	d.pending = false
	d.generation++
	if d.timer != nil {
		d.timer.Stop()
		d.timer = nil
	}
}
