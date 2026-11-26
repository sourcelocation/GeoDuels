package curation

import (
	"context"
	"errors"
	"slices"
	"testing"
	"time"
)

// fakeStore stages changes in a copy and keeps them only when the callback
// succeeds. Unused Store methods panic via the embedded nil interface.
type fakeStore struct {
	Store
	cycle Cycle
	// nominations are most liked first; trending is the map nominated when there are none.
	nominations  []Winner
	trending     Winner
	badgeErr     error
	awarded      []string
	awardSources []string
	cycleAwards  []time.Time
}

func (f *fakeStore) WithinTx(_ context.Context, fn func(Store) error) error {
	tx := *f
	tx.awarded = slices.Clone(f.awarded)
	tx.awardSources, tx.cycleAwards = slices.Clone(f.awardSources), slices.Clone(f.cycleAwards)
	tx.nominations = slices.Clone(f.nominations)
	if err := fn(&tx); err != nil {
		return err
	}
	*f = tx
	return nil
}
func (f *fakeStore) LockCycle(context.Context) (Cycle, error) { return f.cycle, nil }
func (f *fakeStore) Winner(context.Context) (Winner, bool, error) {
	if len(f.nominations) == 0 {
		return Winner{}, false, nil
	}
	return f.nominations[0], true, nil
}
func (f *fakeStore) NominateTrending(context.Context) (bool, error) {
	if f.trending.MapID == "" {
		return false, nil
	}
	nomination := f.trending
	nomination.NominationID, nomination.FromTrending = 99, true
	f.nominations = append(f.nominations, nomination)
	return true, nil
}
func (f *fakeStore) RemoveNomination(_ context.Context, id int64) error {
	f.nominations = slices.DeleteFunc(f.nominations, func(w Winner) bool { return w.NominationID == id })
	return nil
}
func (f *fakeStore) SaveAward(_ context.Context, start, _ time.Time, winner Winner) error {
	f.cycleAwards = append(f.cycleAwards, start)
	f.awardSources = append(f.awardSources, winner.Source())
	return nil
}
func (f *fakeStore) AdvanceCycle(_ context.Context, cycle Cycle, _ time.Time) error {
	f.cycle = cycle
	return nil
}
func (f *fakeStore) AwardBadge(_ context.Context, id, badge string) error {
	if f.badgeErr != nil {
		return f.badgeErr
	}
	f.awarded = append(f.awarded, id+":"+badge)
	return nil
}

func TestCurationClosesOneCycleAcrossDowntime(t *testing.T) {
	for _, source := range []string{"nomination", "trending", "none"} {
		t.Run(source, func(t *testing.T) {
			f := &fakeStore{}
			start := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
			f.cycle = Cycle{StartsAt: start, ClosesAt: start.Add(Window)}
			winner := Winner{NominationID: 1, MapID: "map1", CreatorID: "creator"}
			if source == "nomination" {
				f.nominations = []Winner{winner}
				f.trending = Winner{MapID: "other"}
			}
			if source == "trending" {
				f.trending = winner
			}
			now := start.Add(3*Window + time.Hour)
			svc := NewService(f)
			svc.clock = func() time.Time { return now }
			if err := svc.RunSweep(context.Background()); err != nil {
				t.Fatal(err)
			}
			wantAwards := 1
			if source == "none" {
				wantAwards = 0
			}
			if len(f.cycleAwards) != wantAwards || len(f.awarded) != wantAwards {
				t.Fatalf("awards=%v badges=%v", f.cycleAwards, f.awarded)
			}
			if wantAwards == 1 && (f.awardSources[0] != source || !f.cycleAwards[0].Equal(start)) {
				t.Fatal("wrong winner source or cycle")
			}
			if f.cycle.StartsAt.After(now) || !f.cycle.ClosesAt.After(now) {
				t.Fatalf("invalid next cycle: %+v", f.cycle)
			}
			if err := svc.RunSweep(context.Background()); err != nil {
				t.Fatal(err)
			}
			if len(f.cycleAwards) != wantAwards {
				t.Fatal("sweep repeated award")
			}
		})
	}
}

func TestCurationBadgeFailureLeavesCycleUnchanged(t *testing.T) {
	f := &fakeStore{}
	f.cycle = Cycle{StartsAt: time.Now().Add(-2 * Window), ClosesAt: time.Now().Add(-Window)}
	original := f.cycle
	f.nominations = []Winner{{NominationID: 1, MapID: "map1", CreatorID: "creator"}}
	f.badgeErr = errors.New("badge failed")
	if err := NewService(f).RunSweep(context.Background()); !errors.Is(err, f.badgeErr) {
		t.Fatal(err)
	}
	if f.cycle != original || len(f.cycleAwards) != 0 || len(f.nominations) != 1 {
		t.Fatal("failed award advanced the cycle")
	}
}

// A closing cycle removes only the nomination it awards; the others wait for the next week.
func TestCurationKeepsTheNominationsThatDidNotWin(t *testing.T) {
	start := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
	f := &fakeStore{cycle: Cycle{StartsAt: start, ClosesAt: start.Add(Window)}}
	f.nominations = []Winner{{NominationID: 1, MapID: "map1", CreatorID: "a", Likes: 3}, {NominationID: 2, MapID: "map2", CreatorID: "b"}}
	svc := NewService(f)
	svc.clock = func() time.Time { return start.Add(Window + time.Minute) }
	if err := svc.RunSweep(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(f.nominations) != 1 || f.nominations[0].MapID != "map2" {
		t.Fatalf("nominations after the award: %+v", f.nominations)
	}
	if len(f.awarded) != 1 || f.awarded[0] != "a:map-of-the-week" {
		t.Fatalf("awarded %v", f.awarded)
	}
}
