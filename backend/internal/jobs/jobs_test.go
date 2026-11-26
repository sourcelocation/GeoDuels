package jobs

import (
	"context"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/riverqueue/river"
)

type reachedDatabase struct{}

// noDatabaseTx ends every insert at the point it would have talked to Postgres.
type noDatabaseTx struct{ pgx.Tx }

func (noDatabaseTx) Conn() *pgx.Conn { panic(reachedDatabase{}) }

// insertReachesDatabase runs an insert and says whether River let it get that far.
func insertReachesDatabase(insert func() error) (reached bool, err error) {
	defer func() {
		if recovered := recover(); recovered != nil {
			if recovered != (reachedDatabase{}) {
				panic(recovered)
			}
			reached = true
		}
	}()
	return false, insert()
}

// A process with workers for its own queue still has to be able to ask for anyone else's jobs: signing in
// asks for a Discord sync from the API, which only works the live queue.
func TestAClientWithWorkersInsertsOtherQueuesJobs(t *testing.T) {
	pool, err := pgxpool.New(context.Background(), "postgres://unused@127.0.0.1:1/unused")
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	workers := river.NewWorkers()
	river.AddWorker(workers, river.WorkFunc(func(context.Context, *river.Job[InboxChangedArgs]) error { return nil }))
	client, err := NewClient(pool, QueueLive, workers)
	if err != nil {
		t.Fatal(err)
	}
	reached, err := insertReachesDatabase(func() error {
		return client.EnqueueDiscordSync(context.Background(), noDatabaseTx{}, "sync", "123")
	})
	if !reached {
		t.Fatalf("inserting another queue's job: %v", err)
	}
}

// River enqueues periodic jobs only on its elected leader, which can be any process with workers. The API,
// which works only the live queue, won the election after a rollout and the leaderboard stopped refreshing.
func TestEveryWorkerClientCarriesTheMaintenanceSchedule(t *testing.T) {
	schedule := len(periodicJobs())
	if schedule == 0 {
		t.Fatal("the maintenance schedule is empty")
	}
	for _, queue := range []string{QueueLive, QueueDiscord, river.QueueDefault} {
		if got := len(clientConfig(queue, river.NewWorkers()).PeriodicJobs); got != schedule {
			t.Errorf("a client working %q schedules %d of the %d maintenance jobs", queue, got, schedule)
		}
	}
	if got := len(clientConfig("", nil).PeriodicJobs); got != 0 {
		t.Errorf("an insert-only client schedules %d periodic jobs", got)
	}
}
