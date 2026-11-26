package jobs

import (
	"time"

	"github.com/riverqueue/river"
	"github.com/riverqueue/river/rivertype"

	"geoduels/internal/envcfg"
)

// periodicJobs is the maintenance schedule. River enqueues periodic jobs only on its elected leader,
// and any process with a worker client can win that election, so every worker client carries the
// same schedule; the intervals come from the configuration all services share. The jobs go to the
// default queue, which only the moderation worker works. Each job is unique until it finishes, so
// a change of leader cannot double-run it. River requires the unique states to include available,
// pending, running and scheduled.
func periodicJobs() []*river.PeriodicJob {
	unique := &river.InsertOpts{UniqueOpts: river.UniqueOpts{
		ByArgs: true,
		ByState: []rivertype.JobState{
			rivertype.JobStateAvailable, rivertype.JobStatePending, rivertype.JobStateRetryable,
			rivertype.JobStateRunning, rivertype.JobStateScheduled,
		},
	}}
	every := func(d time.Duration, id string, args river.JobArgs) *river.PeriodicJob {
		return river.NewPeriodicJob(river.PeriodicInterval(d),
			func() (river.JobArgs, *river.InsertOpts) { return args, unique },
			&river.PeriodicJobOpts{ID: id, RunOnStart: false})
	}
	return []*river.PeriodicJob{
		every(interval("GUEST_ACCOUNT_CLEANUP_INTERVAL", time.Hour), "guest_cleanup", GuestCleanupArgs{}),
		every(interval("STORAGE_CLEANUP_INTERVAL", time.Hour), "storage_cleanup", StorageCleanupArgs{}),
		every(time.Minute, "season_reset", SeasonResetArgs{}),
		every(time.Minute, "curation_sweep", CurationSweepArgs{}),
		every(time.Minute, "leaderboard_refresh", LeaderboardRefreshArgs{}),
		every(interval("MAP_STATS_INTERVAL", 5*time.Minute), "map_stats", MapStatsArgs{}),
	}
}

// interval reads a configured interval, keeping the fallback for one that isn't positive.
func interval(key string, fallback time.Duration) time.Duration {
	if d := envcfg.Duration(key, fallback); d > 0 {
		return d
	}
	return fallback
}
