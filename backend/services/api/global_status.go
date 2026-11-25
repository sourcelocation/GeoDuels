package main

import (
	"context"
	"sync/atomic"
	"time"

	"geoduels/internal/presence"
	"geoduels/pkg/maintenance"
	"geoduels/pkg/observability"
)

const statusRefreshEvery = 10 * time.Second

// statusCache is the online count and maintenance status, read at most this often however many ask.
type statusCache struct {
	api   *api
	value atomic.Pointer[statusEvent]
}

func newStatusCache(a *api) *statusCache {
	cache := &statusCache{api: a}
	cache.value.Store(&statusEvent{Maintenance: maintenance.DefaultStatus()})
	return cache
}

func (s *statusCache) current() statusEvent { return *s.value.Load() }

func (s *statusCache) run() {
	s.refresh()
	ticker := time.NewTicker(statusRefreshEvery)
	defer ticker.Stop()
	for range ticker.C {
		s.refresh()
	}
}

func (s *statusCache) refresh() {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	online, err := presence.Count(ctx, s.api.db.Pool())
	if err != nil {
		return
	}
	s.value.Store(&statusEvent{OnlinePlayers: online, Maintenance: maintenance.Current(s.api.settings)})
}

// settingsChanged rereads this process's copy of the site settings after it wrote one, so it follows
// the change at once; other processes see it within sitesettings.RefreshEvery.
func (a *api) settingsChanged(ctx context.Context) {
	if err := a.settings.Refresh(ctx); err != nil {
		observability.Log("warn", "site settings refresh failed", map[string]any{"error": err.Error()})
	}
	a.status.refresh()
}
