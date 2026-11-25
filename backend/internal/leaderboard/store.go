package leaderboard

import (
	"context"

	"github.com/jackc/pgx/v5/pgxpool"

	"geoduels/internal/storekit"
	db "geoduels/pkg/persistence/sqlc/db"
)

const modeDuel = "duel"

// PGStore owns PostgreSQL access for the leaderboard feature.
type PGStore struct {
	pool *pgxpool.Pool
	// settings is the process's copy of the site settings, for the season shown when none is asked for.
	settings storekit.Settings
}

func NewPGStore(pool *pgxpool.Pool, settings storekit.Settings) *PGStore {
	return &PGStore{pool: pool, settings: settings}
}

func (s *PGStore) q() *db.Queries { return db.New(s.pool) }

func (s *PGStore) ListLeaderboard(ctx context.Context, mode, seasonID string, limit, offset int) ([]Entry, error) {
	if mode == "" {
		mode = modeDuel
	}
	if limit <= 0 {
		limit = 100
	}
	if limit > 200 {
		limit = 200
	}
	if offset < 0 {
		offset = 0
	}
	if seasonID == "" {
		seasonID = storekit.CurrentSeasonID(s.settings)
	}
	rows, err := s.q().ListLeaderboard(ctx, db.ListLeaderboardParams{Mode: db.GdMatchMode(mode), SeasonID: seasonID, RowOffset: int32(offset), RowLimit: int32(limit)})
	if err != nil {
		return nil, err
	}
	entries := make([]Entry, 0, limit)
	for _, row := range rows {
		entries = append(entries, Entry{Rank: int(row.Rank), UserID: storekit.UUIDVal(row.UserID), DisplayName: row.DisplayName.String, AvatarURL: row.AvatarUrl, MMR: int(row.Mmr), GamesPlayed: int(row.GamesPlayed), Wins: int(row.Wins)})
	}
	return entries, nil
}

func (s *PGStore) GetLeaderboardOverview(ctx context.Context, userID, mode, seasonID string, limit int) (Overview, error) {
	if mode == "" {
		mode = modeDuel
	}
	if limit <= 0 {
		limit = 10
	}
	if limit > 100 {
		limit = 100
	}
	entries, err := s.ListLeaderboard(ctx, mode, seasonID, limit, 0)
	if err != nil {
		return Overview{}, err
	}
	if seasonID == "" {
		seasonID = storekit.CurrentSeasonID(s.settings)
	}
	totals, err := s.q().GetLeaderboardTotals(ctx, db.GetLeaderboardTotalsParams{SelfUserID: userID, Mode: db.GdMatchMode(mode), SeasonID: seasonID})
	if err != nil {
		return Overview{}, err
	}
	return Overview{Mode: mode, SeasonID: seasonID, SelfRank: int(totals.SelfRank), TotalPlayers: int(totals.TotalPlayers), Entries: entries}, nil
}

// Refresh moves a season's players to their places on its leaderboard, the active season when seasonID
// is empty, and says how many moved.
func (s *PGStore) Refresh(ctx context.Context, seasonID string) (int, error) {
	if seasonID == "" {
		var err error
		seasonID, err = storekit.ActiveSeasonID(ctx, s.pool)
		if err != nil {
			return 0, err
		}
	}
	moved, err := s.q().RefreshLeaderboard(ctx, db.RefreshLeaderboardParams{Mode: modeDuel, SeasonID: seasonID})
	return int(moved), err
}
