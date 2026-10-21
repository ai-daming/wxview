package daemon

import (
	"fmt"
	"os"
	"sync"
	"time"

	"wxview/internal/key"
)

// discoverySnapshot separates expensive account probing from lightweight stats.
// A single mutex coalesces concurrent expiry checks, including failed probes.
type DiscoveryStats struct {
	AccountProbes     uint64 `json:"account_probes"`
	ProbeFailures     uint64 `json:"probe_failures"`
	ProbeMilliseconds int64  `json:"probe_milliseconds"`
	DirectoryScans    uint64 `json:"directory_scans"`
}

type discoverySnapshot struct {
	statsMu     sync.Mutex
	stats       DiscoveryStats
	mu          sync.Mutex
	discover    func() (key.TargetDB, error)
	now         func() time.Time
	nextAccount time.Time
	nextPaths   time.Time
	account     key.TargetDB
	paths       []key.TargetDB
	err         error
}

func (d *discoverySnapshot) get() (key.TargetDB, []key.TargetDB, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	now := time.Now()
	if d.now != nil {
		now = d.now()
	}
	if !now.Before(d.nextAccount) {
		discover := d.discover
		if discover == nil {
			discover = key.DiscoverWatcherAccount
		}
		started := time.Now()
		account, err := discover()
		d.statsMu.Lock()
		d.stats.AccountProbes++
		d.stats.ProbeMilliseconds += time.Since(started).Milliseconds()
		if err != nil {
			d.stats.ProbeFailures++
		}
		d.statsMu.Unlock()
		// Also cache failures: six watchers must not launch six retry storms.
		d.nextAccount = now.Add(20 * time.Second)
		d.err = err
		if err == nil {
			if account.Account != d.account.Account || account.DataDir != d.account.DataDir {
				d.nextPaths = time.Time{}
				d.paths = nil
			}
			d.account = account
		}
	}
	if d.err != nil {
		return key.TargetDB{}, nil, d.err
	}
	if d.account.Account == "" {
		return key.TargetDB{}, nil, fmt.Errorf("no current account")
	}
	if !now.Before(d.nextPaths) {
		d.statsMu.Lock()
		d.stats.DirectoryScans++
		d.statsMu.Unlock()
		paths, err := key.AccountMessageTargets(d.account)
		// Missing message directories are normal during login; retain the account,
		// retry directory discovery independently from the expensive process probe.
		if err == nil {
			d.paths = paths
		} else {
			d.paths = nil
		}
		d.nextPaths = now.Add(5 * time.Second)
	}
	return d.account, append([]key.TargetDB(nil), d.paths...), nil
}

func (s *Server) watchPaths(relative string) []string {
	account, targets, err := s.discovery.get()
	if err != nil {
		return nil
	}
	if relative != "messages" {
		return []string{key.TargetFor(account, relative).DBPath}
	}
	paths := make([]string, 0, len(targets))
	for _, target := range targets {
		paths = append(paths, target.DBPath)
	}
	return paths
}

func existingTarget(account key.TargetDB, relative string) (key.TargetDB, error) {
	target := key.TargetFor(account, relative)
	if info, err := os.Stat(target.DBPath); err != nil || info.IsDir() {
		return key.TargetDB{}, fmt.Errorf("database %s is unavailable for the selected account", relative)
	}
	return target, nil
}

func (d *discoverySnapshot) statistics() DiscoveryStats {
	d.statsMu.Lock()
	defer d.statsMu.Unlock()
	return d.stats
}
