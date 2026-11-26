// Package queue holds the players waiting for a ranked duel and decides who plays whom. Which queues
// are offered is a setting (Offered); a player waits in one or more of them at once, and a pairing
// takes both players out of all of them. The matchmaker owns the queue: it is the only writer.
package queue

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"geoduels/internal/rating"
	"geoduels/internal/storekit"
	db "geoduels/pkg/persistence/sqlc/db"
)

// Ticket is a player waiting, in every queue they chose.
type Ticket struct {
	UserID string
	Queues []string
	MMR    int
	RD     float64
	// LastOpponentID is whom the player last played in a rated match.
	LastOpponentID string
	JoinedAt       time.Time
}

type PGStore struct {
	pool *pgxpool.Pool
	db   *db.Queries
}

func NewPGStore(pool *pgxpool.Pool) *PGStore {
	return &PGStore{pool: pool, db: db.New(pool)}
}

// Join queues the player in these queues at their rating in the season, returning when they joined.
// Queueing again only changes the queues: the player keeps their place.
func (s *PGStore) Join(ctx context.Context, userID string, queues []string, seasonID string) (time.Time, error) {
	id, err := storekit.ProfileUUID(userID)
	if err != nil {
		return time.Time{}, err
	}
	joinedAt, err := s.db.JoinQueue(ctx, db.JoinQueueParams{
		UserID: id, Queues: queues, SeasonID: seasonID, DefaultMmr: rating.InitialMMR, DefaultRd: rating.InitialRatingRD,
	})
	return joinedAt.Time, err
}

// Leave takes the player out of the queue, reporting whether they were in it.
func (s *PGStore) Leave(ctx context.Context, userID string) (bool, error) {
	id, err := storekit.ProfileUUID(userID)
	if err != nil {
		return false, err
	}
	left, err := s.db.LeaveQueue(ctx, id)
	return left > 0, err
}

// QueuedSince is when the player joined the queue, if they're in it.
func (s *PGStore) QueuedSince(ctx context.Context, userID string) (time.Time, bool, error) {
	id, err := storekit.ProfileUUID(userID)
	if err != nil {
		return time.Time{}, false, err
	}
	joinedAt, err := s.db.QueueTicketJoinedAt(ctx, id)
	if errors.Is(err, pgx.ErrNoRows) {
		return time.Time{}, false, nil
	}
	return joinedAt.Time, err == nil, err
}

// TakeTx takes these players out of the queue in the caller's transaction, returning who was in it.
// Every match start takes its players' tickets before it seats them, so a pairing pass holding a
// ticket and a start for the same player lock in the same order.
func TakeTx(ctx context.Context, tx pgx.Tx, userIDs []string) ([]string, error) {
	rows, err := db.New(tx).TakeQueueTickets(ctx, storekit.UUIDList(userIDs))
	return uuidStrings(rows), err
}

// LockWaitingTx locks the tickets of connected players that no other pass holds, longest-waiting
// first, until the caller's transaction ends.
func LockWaitingTx(ctx context.Context, tx pgx.Tx) ([]Ticket, error) {
	rows, err := db.New(tx).LockWaitingTickets(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]Ticket, len(rows))
	for i, row := range rows {
		out[i] = Ticket{
			UserID: storekit.UUIDVal(row.UserID), Queues: row.Queues, MMR: int(row.Mmr), RD: row.Rd,
			LastOpponentID: storekit.UUIDVal(row.LastOpponentID), JoinedAt: row.JoinedAt.Time,
		}
	}
	return out, nil
}

// DropDisconnectedTx takes out the players with no connection left, returning who they were.
func DropDisconnectedTx(ctx context.Context, tx pgx.Tx) ([]string, error) {
	rows, err := db.New(tx).DropDisconnectedTickets(ctx)
	return uuidStrings(rows), err
}

func uuidStrings(ids []pgtype.UUID) []string {
	out := make([]string, len(ids))
	for i, id := range ids {
		out[i] = storekit.UUIDVal(id)
	}
	return out
}
