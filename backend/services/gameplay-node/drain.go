package main

import (
	"sync"
	"time"

	"geoduels/pkg/maintenance"
)

// gameplayDrain keeps the original drain start across maintenance and SIGTERM.
// Maintenance alone must not exit the process or make replacement pods unready.
type gameplayDrain struct {
	mu        sync.Mutex
	startedAt time.Time
	stopping  bool
}

func (d *gameplayDrain) setMaintenance(status maintenance.Status, now time.Time) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.stopping {
		return
	}
	if status.Phase == maintenance.PhaseActive || status.PlayBlocked() {
		if d.startedAt.IsZero() {
			d.startedAt = now
		}
	} else {
		d.startedAt = time.Time{}
	}
}

func (d *gameplayDrain) startShutdown(now time.Time) time.Time {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.stopping = true
	if d.startedAt.IsZero() {
		d.startedAt = now
	}
	return d.startedAt
}

func (d *gameplayDrain) isDraining() bool {
	d.mu.Lock()
	defer d.mu.Unlock()
	return !d.startedAt.IsZero()
}

func (d *gameplayDrain) isStopping() bool {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.stopping
}

// refreshMaintenanceDrain drains the node while maintenance blocks play, as the process's copy of the
// settings has it.
func (g *gameplayNode) refreshMaintenanceDrain() {
	g.drain.setMaintenance(maintenance.Current(g.settings), time.Now())
}
