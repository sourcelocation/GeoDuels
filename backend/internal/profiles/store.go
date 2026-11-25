package profiles

import (
	"context"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"geoduels/internal/storekit"
	db "geoduels/pkg/persistence/sqlc/db"
)

// PGStore owns PostgreSQL access for this feature.
type PGStore struct {
	pool *pgxpool.Pool
	db   *db.Queries
	// settings is the process's copy of the site settings, for the season profiles are read in.
	settings storekit.Settings
}

func NewPGStore(pool *pgxpool.Pool, settings storekit.Settings) *PGStore {
	return &PGStore{pool: pool, db: db.New(pool), settings: settings}
}

func (s *PGStore) withTx(ctx context.Context, fn func(pgx.Tx) error) error {
	return storekit.WithTx(ctx, s.pool, fn)
}
