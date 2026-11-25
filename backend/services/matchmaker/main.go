package main

import (
	"context"
	"errors"
	"log"
	"net/http"
	"os"
	"os/signal"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"geoduels/internal/envcfg"
	"geoduels/internal/jobs"
	"geoduels/internal/maps"
	"geoduels/internal/matches"
	"geoduels/internal/parties"
	"geoduels/internal/queue"
	"geoduels/pkg/live"
	"geoduels/pkg/observability"
	"geoduels/pkg/persistence"
	"geoduels/pkg/sitesettings"
)

// The matchmaker creates every match. Players ask it directly, under /matchmaking: to wait in the
// ranked queues, or to start a match alone or with their party. It checks that the players may play,
// writes the match and its seats in one transaction and tells them; a gameplay node picks the match
// up from there. It holds nothing a restart would lose: the queue is a table, and any number of
// replicas pair it at once, each skipping the tickets another holds.

const (
	// A pod stops receiving traffic before it stops serving what it already has.
	drainTimeout    = 5 * time.Second
	shutdownTimeout = 10 * time.Second
)

type matchmaker struct {
	db       *persistence.DB
	pool     *pgxpool.Pool
	settings *sitesettings.Copy
	queue    *queue.PGStore
	matches  *matches.PGStore
	maps     *maps.PGStore
	parties  *parties.Service
	jobs     *jobs.Client
	bus      live.Publisher
	secret   []byte
	stopping atomic.Bool
}

func main() {
	m, err := newMatchmaker()
	if err != nil {
		log.Fatal(err)
	}
	defer m.db.Close()
	ctx, stop := context.WithCancel(context.Background())
	var loops sync.WaitGroup
	loops.Add(2)
	go func() { defer loops.Done(); m.settings.Run(ctx) }()
	go func() { defer loops.Done(); m.runPasses(ctx) }()

	addr := envcfg.Get("MATCHMAKER_ADDR", ":8097")
	srv := &http.Server{
		Addr:              addr,
		Handler:           routes(m),
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       10 * time.Second,
		WriteTimeout:      15 * time.Second,
		IdleTimeout:       60 * time.Second,
	}
	observability.Log("info", "matchmaker startup", map[string]any{"addr": addr})
	drained := make(chan struct{})
	go func() {
		defer close(drained)
		signals := make(chan os.Signal, 1)
		signal.Notify(signals, syscall.SIGTERM, syscall.SIGINT)
		<-signals
		m.stopping.Store(true)
		time.Sleep(drainTimeout)
		shutdownCtx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
		defer cancel()
		if err := srv.Shutdown(shutdownCtx); err != nil {
			log.Printf("matchmaker shutdown failed: %v", err)
		}
		stop()
		loops.Wait()
	}()
	if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		log.Fatal(err)
	}
	<-drained
}

func newMatchmaker() (*matchmaker, error) {
	secret, err := envcfg.RequiredSecret("APP_AUTH_SECRET", 32)
	if err != nil {
		return nil, err
	}
	store, err := persistence.NewFromEnv()
	if err != nil {
		return nil, err
	}
	pool := store.Pool()
	loadCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	settings, err := sitesettings.Load(loadCtx, pool)
	if err != nil {
		store.Close()
		return nil, err
	}
	bus, err := live.Connect(os.Getenv("NATS_URL"), func() {})
	if err != nil {
		store.Close()
		return nil, err
	}
	// Insert-only: the API works the live queue that tells parties and friends.
	jobsClient, err := jobs.NewClient(pool, "", nil, nil)
	if err != nil {
		store.Close()
		return nil, err
	}
	mapsStore := maps.NewPGStore(pool, settings)
	return &matchmaker{
		db: store, pool: pool, settings: settings,
		queue:   queue.NewPGStore(pool),
		matches: matches.NewPGStore(pool, nil, jobsClient),
		maps:    mapsStore,
		parties: parties.NewService(parties.NewPGStore(pool, mapsStore)),
		jobs:    jobsClient,
		bus:     bus,
		secret:  secret,
	}, nil
}

// tell publishes an event once what it reports has committed. Losing one only delays what someone
// sees: every connection starts from a full session.
func (m *matchmaker) tell(userIDs []string, event live.Event) {
	if err := m.bus.Tell(userIDs, event); err != nil {
		observability.Log("warn", "live publish failed", map[string]any{"type": event.EventType(), "error": err.Error()})
	}
}
