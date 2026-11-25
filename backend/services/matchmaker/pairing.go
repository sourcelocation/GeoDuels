package main

import (
	"context"
	"math/rand"
	"time"

	"github.com/jackc/pgx/v5"

	"geoduels/internal/queue"
	"geoduels/pkg/maintenance"
	"geoduels/pkg/matchkind"
	"geoduels/pkg/observability"
)

// A pass runs the moment someone joins the queue and every second after, since waiting widens who can
// meet. Any number of replicas run passes at once: each locks the tickets no other pass holds.
const passEvery = time.Second

// runPasses pairs the queue every passEvery until ctx ends.
func (m *matchmaker) runPasses(ctx context.Context) {
	ticker := time.NewTicker(passEvery)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			passCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
			if _, err := m.pass(passCtx); err != nil {
				observability.Log("warn", "matchmaking pass failed", map[string]any{"error": err.Error()})
			}
			cancel()
		}
	}
}

// pass takes out the players who left, pairs the waiting players no other pass holds, and starts their
// matches. A player who can't play any more leaves the queue; their opponent keeps their place.
func (m *matchmaker) pass(ctx context.Context) ([]started, error) {
	tx, err := m.pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)
	if _, err := queue.DropDisconnectedTx(ctx, tx); err != nil {
		return nil, err
	}
	if maintenance.Current(m.settings).QueueBlocked() {
		return nil, tx.Commit(ctx)
	}
	tickets, err := queue.LockWaitingTx(ctx, tx)
	if err != nil {
		return nil, err
	}
	offered := queue.Offered(m.settings)
	waiting := make([]queue.Ticket, 0, len(tickets))
	removed := map[string]string{}
	for _, ticket := range tickets {
		if ticket.Queues = offered.Choose(ticket.Queues); len(ticket.Queues) == 0 {
			removed[ticket.UserID] = "The queue you were waiting in closed, so you left it."
			continue
		}
		waiting = append(waiting, ticket)
	}
	var out []started
	for _, pair := range queue.Pairings(waiting, time.Now(), rand.New(rand.NewSource(time.Now().UnixNano()))) {
		match, refused, err := m.startPair(ctx, tx, pair, offered)
		if err != nil {
			observability.Log("warn", "ranked match start failed", map[string]any{"error": err.Error()})
			continue
		}
		if refused.refused() {
			if refused.UserID != "" {
				removed[refused.UserID] = "You can't play right now, so you left the queue."
			}
			continue
		}
		out = append(out, match)
	}
	gone := make([]string, 0, len(removed))
	for userID := range removed {
		gone = append(gone, userID)
	}
	if _, err := queue.TakeTx(ctx, tx, gone); err != nil {
		return nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	for _, match := range out {
		m.tellStarted(match)
	}
	for userID, reason := range removed {
		m.tell([]string{userID}, queueState{Error: reason})
	}
	return out, nil
}

// startPair starts a pair's match under a savepoint of the pass, so one that can't start leaves the
// rest of the pass as it was.
func (m *matchmaker) startPair(ctx context.Context, tx pgx.Tx, pair queue.Pair, offered queue.Queues) (started, refusal, error) {
	q, _ := offered.Get(pair.Queue)
	savepoint, err := tx.Begin(ctx)
	if err != nil {
		return started{}, refusal{}, err
	}
	defer savepoint.Rollback(ctx)
	match, refused, err := m.startTx(ctx, savepoint, startRequest{
		Kind:   matchkind.RankedDuel,
		Seats:  []matchkind.Seat{{UserID: pair.Second.UserID}, {UserID: pair.First.UserID}},
		Config: q.Config,
	})
	if err != nil || refused.refused() {
		return started{}, refused, err
	}
	return match, refusal{}, savepoint.Commit(ctx)
}
