package accounts

import (
	"context"

	"geoduels/internal/storekit"
	db "geoduels/pkg/persistence/sqlc/db"
)

// Player is what decides whether someone may play now, and how their seat shows them.
type Player struct {
	UserID        string
	DisplayName   string
	AvatarURL     string
	NeedsNickname bool
	// Registered players signed in with an account; the others are guests.
	Registered bool
	Banned     bool
}

// Players reads these players in one query, by user id. Someone without an account isn't in it.
func Players(ctx context.Context, q db.DBTX, userIDs []string) (map[string]Player, error) {
	rows, err := db.New(q).ListPlayers(ctx, storekit.UUIDList(userIDs))
	if err != nil {
		return nil, err
	}
	out := make(map[string]Player, len(rows))
	for _, row := range rows {
		player := Player{
			UserID: storekit.UUIDVal(row.UserID), DisplayName: row.DisplayName, AvatarURL: row.AvatarUrl,
			NeedsNickname: row.NeedsNickname, Registered: row.Registered, Banned: row.Banned,
		}
		out[player.UserID] = player
	}
	return out, nil
}
