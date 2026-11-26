package main

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/riverqueue/river"

	"geoduels/internal/accounts"
	"geoduels/internal/curation"
	"geoduels/internal/jobs"
	"geoduels/internal/leaderboard"
	"geoduels/internal/maps"
	"geoduels/internal/moderation"
	"geoduels/internal/seasons"
	"geoduels/internal/storage"
	"geoduels/pkg/observability"
)

// matchAnalyzeWorker runs the integrity detector for a finished match.
type matchAnalyzeWorker struct {
	river.WorkerDefaults[jobs.MatchAnalyzeArgs]
	moderation *moderation.Service
}

func (w *matchAnalyzeWorker) Work(ctx context.Context, job *river.Job[jobs.MatchAnalyzeArgs]) error {
	if w.moderation == nil || job.Args.MatchID == "" {
		return nil
	}
	return w.moderation.EvaluateMatch(ctx, job.Args.MatchID)
}

// guestCleanupWorker deletes stale guest accounts in batches.
type guestCleanupWorker struct {
	river.WorkerDefaults[jobs.GuestCleanupArgs]
	accounts *accounts.Service
	ttl      time.Duration
	batch    int
}

func (w *guestCleanupWorker) Work(context.Context, *river.Job[jobs.GuestCleanupArgs]) error {
	if w.accounts == nil || w.ttl <= 0 || w.batch <= 0 {
		return nil
	}
	total := 0
	for {
		deleted, err := w.accounts.DeleteGuestAccountsOlderThan(w.ttl, w.batch)
		if err != nil {
			return err
		}
		total += deleted
		if deleted < w.batch {
			break
		}
	}
	if total > 0 {
		observability.Log("info", "guest cleanup completed", map[string]any{"deleted": total})
	}
	return nil
}

// storageCleanupWorker prunes storage.
type storageCleanupWorker struct {
	river.WorkerDefaults[jobs.StorageCleanupArgs]
	storage storage.Store
	batch   int
}

func (w *storageCleanupWorker) Work(ctx context.Context, _ *river.Job[jobs.StorageCleanupArgs]) error {
	if w.storage == nil {
		return nil
	}
	result, err := w.storage.CleanupStorage(w.batch)
	if err != nil {
		return err
	}
	if result != (storage.StorageCleanupResult{}) {
		observability.Log("info", "storage cleanup completed", map[string]any{
			"expired_replays":   result.ExpiredReplays,
			"match_sessions":    result.MatchSessions,
			"chat_messages":     result.ChatMessages,
			"auth_sessions":     result.AuthSessions,
			"parties":           result.Parties,
			"map_upload_events": result.MapUploadEvents,
			"inbox_messages":    result.InboxMessages,
		})
	}
	return nil
}

// seasonResetWorker applies a due ranked season reset.
type seasonResetWorker struct {
	river.WorkerDefaults[jobs.SeasonResetArgs]
	seasons     seasons.Store
	leaderboard *leaderboard.Service
}

func (w *seasonResetWorker) Work(ctx context.Context, _ *river.Job[jobs.SeasonResetArgs]) error {
	if w.seasons == nil {
		return nil
	}
	result, reset, err := w.seasons.RunDueRankedSeasonReset(time.Now().UTC())
	if err != nil {
		return err
	}
	if !reset {
		return nil
	}
	observability.Log("info", "ranked season reset completed", map[string]any{
		"previousSeasonId": result.PreviousSeasonID,
		"activeSeasonId":   result.ActiveSeasonID,
		"playersSeeded":    result.PlayersSeeded,
	})
	// The finished season's leaderboard is final, and the new one has its players from the start.
	for _, season := range []string{result.PreviousSeasonID, result.ActiveSeasonID} {
		if _, err := w.leaderboard.Refresh(ctx, season); err != nil {
			return err
		}
	}
	return nil
}

// leaderboardRefreshWorker keeps the active season's leaderboard places current.
type leaderboardRefreshWorker struct {
	river.WorkerDefaults[jobs.LeaderboardRefreshArgs]
	leaderboard *leaderboard.Service
}

func (w *leaderboardRefreshWorker) Work(ctx context.Context, _ *river.Job[jobs.LeaderboardRefreshArgs]) error {
	_, err := w.leaderboard.Refresh(ctx, "")
	return err
}

// curationSweepWorker closes any due Map-of-the-Week cycle.
type curationSweepWorker struct {
	river.WorkerDefaults[jobs.CurationSweepArgs]
	curation *curation.Service
}

func (w *curationSweepWorker) Work(ctx context.Context, _ *river.Job[jobs.CurationSweepArgs]) error {
	if w.curation == nil {
		return nil
	}
	return w.curation.RunSweep(ctx)
}

// mapStatsWorker recounts the maps' plays and trending scores from match history.
type mapStatsWorker struct {
	river.WorkerDefaults[jobs.MapStatsArgs]
	pool *pgxpool.Pool
}

func (w *mapStatsWorker) Work(ctx context.Context, _ *river.Job[jobs.MapStatsArgs]) error {
	result, err := maps.RefreshStats(ctx, w.pool)
	if err != nil {
		return err
	}
	if result != (maps.StatsRefresh{}) {
		observability.Log("info", "map stats refreshed", map[string]any{"trending": result.Trending, "plays": result.Plays})
	}
	return nil
}
