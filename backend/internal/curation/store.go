package curation

import (
	"context"
	"errors"
	"geoduels/internal/jobs"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"geoduels/internal/badges"
	"geoduels/internal/storekit"
	db "geoduels/pkg/persistence/sqlc/db"
)

// Store persists curation state. WithinTx supplies a store whose writes commit
// together, or roll back when the callback returns an error.
type Store interface {
	WithinTx(ctx context.Context, fn func(Store) error) error
	LockCycle(ctx context.Context) (Cycle, error)
	Winner(ctx context.Context) (Winner, bool, error)
	NominateTrending(ctx context.Context) (bool, error)
	RemoveNomination(ctx context.Context, id int64) error
	SaveAward(ctx context.Context, start, now time.Time, winner Winner) error
	AdvanceCycle(ctx context.Context, cycle Cycle, awardedAt time.Time) error
	AwardBadge(ctx context.Context, userID, badgeID string) error
	NominateMap(ctx context.Context, actorID, mapID string) error
	SetNominationLike(ctx context.Context, actorID string, id int64, liked bool) error
	ListNominations(ctx context.Context, actorID string, page int, closesAt time.Time) (Page, error)
}

type PGStore struct {
	pool   *pgxpool.Pool
	tx     pgx.Tx
	teller jobs.Teller
}

func NewPGStore(pool *pgxpool.Pool, teller jobs.Teller) *PGStore {
	return &PGStore{pool: pool, teller: teller}
}

// q selects the transaction when present and the pool otherwise.
func (a *PGStore) q() *db.Queries {
	if a.tx != nil {
		return db.New(a.tx)
	}
	return db.New(a.pool)
}

func (a *PGStore) requireTx() (pgx.Tx, error) {
	if a.tx == nil {
		return nil, errors.New("curation store: operation requires a transaction")
	}
	return a.tx, nil
}

// WithinTx runs fn against a copy bound to the current transaction.
func (a *PGStore) WithinTx(ctx context.Context, fn func(Store) error) error {
	return storekit.WithTx(ctx, a.pool, func(tx pgx.Tx) error {
		bound := *a
		bound.tx = tx
		return fn(&bound)
	})
}

var _ Store = (*PGStore)(nil)

func stamp(t time.Time) pgtype.Timestamptz {
	return pgtype.Timestamptz{Time: t, Valid: true}
}

func (a *PGStore) LockCycle(ctx context.Context) (Cycle, error) {
	if _, err := a.requireTx(); err != nil {
		return Cycle{}, err
	}
	row, err := a.q().LockMOTWCycle(ctx)
	return Cycle{StartsAt: row.StartsAt.Time, ClosesAt: row.ClosesAt.Time}, err
}

func (a *PGStore) Winner(ctx context.Context) (Winner, bool, error) {
	row, err := a.q().MOTWWinner(ctx)
	if errors.Is(err, pgx.ErrNoRows) {
		return Winner{}, false, nil
	}
	return Winner{
		NominationID: row.ID, MapID: storekit.UUIDVal(row.MapID), Name: row.DisplayName, CreatorID: storekit.UUIDVal(row.OwnerUserID),
		Likes: int(row.Likes), FromTrending: row.FromTrending,
	}, err == nil, err
}

// NominateTrending nominates the top trending map that never won, reporting whether there was one.
func (a *PGStore) NominateTrending(ctx context.Context) (bool, error) {
	nominated, err := a.q().NominateTrendingMOTWMap(ctx)
	return nominated > 0, err
}

func (a *PGStore) RemoveNomination(ctx context.Context, id int64) error {
	return a.q().DeleteMOTWNomination(ctx, id)
}

func (a *PGStore) SaveAward(ctx context.Context, start, now time.Time, winner Winner) error {
	return a.q().InsertMOTWAward(ctx, db.InsertMOTWAwardParams{
		CycleStart: stamp(start), SelectedAt: stamp(now), MapID: storekit.MustUUID(winner.MapID), MapName: winner.Name,
		CreatorUserID: storekit.MustUUID(winner.CreatorID), Source: winner.Source(), Likes: int32(winner.Likes),
	})
}

func (a *PGStore) AdvanceCycle(ctx context.Context, cycle Cycle, awardedAt time.Time) error {
	award := pgtype.Timestamptz{Time: awardedAt, Valid: !awardedAt.IsZero()}
	return a.q().AdvanceMOTWCycle(ctx, db.AdvanceMOTWCycleParams{StartsAt: stamp(cycle.StartsAt), ClosesAt: stamp(cycle.ClosesAt), CurrentAwardAt: award})
}

// AwardBadge awards the creator's badge in the cycle transaction.
func (a *PGStore) AwardBadge(ctx context.Context, userID, badgeID string) error {
	tx, err := a.requireTx()
	if err != nil {
		return err
	}
	_, err = badges.AwardBadgeTx(ctx, tx, a.teller, userID, badgeID)
	return err
}

func (a *PGStore) NominateMap(ctx context.Context, actorID, mapID string) error {
	user, err := storekit.ProfileUUID(actorID)
	if err != nil {
		return err
	}
	id, err := storekit.ProfileUUID(mapID)
	if err != nil {
		return ErrUnavailable
	}
	_, err = a.q().NominateMOTWMap(ctx, db.NominateMOTWMapParams{ActorID: user, MapID: id})
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrUnavailable
	}
	return err
}

func (a *PGStore) SetNominationLike(ctx context.Context, actorID string, id int64, liked bool) error {
	user, err := storekit.ProfileUUID(actorID)
	if err != nil {
		return err
	}
	q := a.q()
	exists, err := q.MOTWNominationExists(ctx, id)
	if err != nil {
		return err
	}
	if !exists {
		return ErrUnavailable
	}
	if liked {
		return q.SetMOTWLike(ctx, db.SetMOTWLikeParams{NominationID: id, ActorID: user})
	}
	return q.RemoveMOTWLike(ctx, db.RemoveMOTWLikeParams{NominationID: id, ActorID: user})
}

func (a *PGStore) ListNominations(ctx context.Context, actorID string, page int, closesAt time.Time) (Page, error) {
	result := Page{Items: []Nomination{}, Page: page, PageSize: 20, ClosesAt: closesAt}
	user, err := storekit.ProfileUUID(actorID)
	if err != nil {
		return result, err
	}
	q := a.q()
	count, err := q.CountMOTWNominations(ctx)
	if err != nil {
		return Page{}, err
	}
	result.Total = int(count)
	rows, err := q.ListMOTWNominations(ctx, db.ListMOTWNominationsParams{ActorID: user, PageSize: 20, PageOffset: int32((page - 1) * 20)})
	if err != nil {
		return Page{}, err
	}
	for _, v := range rows {
		result.Items = append(result.Items, Nomination{
			ID: v.ID, MapID: v.MapID.String(), Name: v.DisplayName, ThumbnailKey: storekit.TextVal(v.ThumbnailKey),
			AuthorName: v.AuthorName, Likes: int(v.Likes), Liked: v.Liked, NominatedAt: v.NominatedAt.Time,
		})
	}
	return result, nil
}
