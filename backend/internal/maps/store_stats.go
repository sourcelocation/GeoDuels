package maps

import (
	"context"

	"github.com/jackc/pgx/v5"

	db "geoduels/pkg/persistence/sqlc/db"
)

// A favorite or a comment counts toward its map at once. Plays and the trending score come from
// match history: RefreshStats recounts them every few minutes, so nothing a match does writes its map.

func incrementMapFavoriteStats(c context.Context, t pgx.Tx, m string) error {
	id, e := mapUUID(m)
	if e != nil {
		return e
	}
	return db.New(t).IncrementMapFavorite(c, id)
}

func incrementMapCommentStats(c context.Context, t pgx.Tx, m string) error {
	id, e := mapUUID(m)
	if e != nil {
		return e
	}
	return db.New(t).IncrementMapComment(c, id)
}

// StatsRefresh is how many maps a refresh changed.
type StatsRefresh struct {
	Trending int64
	Plays    int64
}

// RefreshStats recounts every map's trending score from its last week, and the plays of the maps
// played in it, from match history, favorites and comments.
func RefreshStats(ctx context.Context, q db.DBTX) (StatsRefresh, error) {
	var out StatsRefresh
	var err error
	if out.Trending, err = db.New(q).RefreshMapTrending(ctx); err != nil {
		return out, err
	}
	out.Plays, err = db.New(q).RefreshMapPlays(ctx)
	return out, err
}
