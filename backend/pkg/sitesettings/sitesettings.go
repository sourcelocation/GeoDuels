// Package sitesettings is a process's copy of the site settings: maintenance, the ranked season, the
// gameplay maps and the ranked queues. Each process reads them all in one query when it starts and
// once a minute after. A process that writes a setting refreshes its own copy at once; the others see
// the change within the minute. Each setting's owner decodes its own key from a copy.
package sitesettings

import (
	"context"
	"log"
	"sync/atomic"
	"time"

	db "geoduels/pkg/persistence/sqlc/db"
)

// RefreshEvery is how far behind the database a copy may be.
const RefreshEvery = time.Minute

// Copy holds the settings as last read.
type Copy struct {
	q      db.DBTX
	values atomic.Pointer[map[string][]byte]
}

// Load reads every setting, so a process never starts on settings it hasn't read.
func Load(ctx context.Context, q db.DBTX) (*Copy, error) {
	c := &Copy{q: q}
	if err := c.Refresh(ctx); err != nil {
		return nil, err
	}
	return c, nil
}

// Refresh reads every setting again.
func (c *Copy) Refresh(ctx context.Context) error {
	rows, err := db.New(c.q).ListSettings(ctx)
	if err != nil {
		return err
	}
	values := make(map[string][]byte, len(rows))
	for _, row := range rows {
		values[row.Key] = row.ValueJson
	}
	c.values.Store(&values)
	return nil
}

// Run refreshes the copy every RefreshEvery until ctx ends. A failed read keeps the last copy.
func (c *Copy) Run(ctx context.Context) {
	ticker := time.NewTicker(RefreshEvery)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			readCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
			if err := c.Refresh(readCtx); err != nil {
				log.Printf("site settings refresh failed: %v", err)
			}
			cancel()
		}
	}
}

// Value is a setting's JSON; false when it isn't set.
func (c *Copy) Value(key string) ([]byte, bool) {
	value, ok := (*c.values.Load())[key]
	return value, ok
}
