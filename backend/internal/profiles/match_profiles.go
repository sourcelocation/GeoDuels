package profiles

import (
	"context"

	badgekit "geoduels/internal/badges"
	"geoduels/internal/storekit"
	"geoduels/pkg/gameplay"
	db "geoduels/pkg/persistence/sqlc/db"
)

// MatchProfiles is what a match shows of its players and rates them by, read once by the node that
// runs it: names, avatars, ratings and ranked games in the season, and the badge each shows. A player
// without an account plays under their id at the initial rating.
func MatchProfiles(ctx context.Context, q db.DBTX, userIDs []string, seasonID string) (map[string]gameplay.PlayerProfile, error) {
	rows, err := db.New(q).ListMatchProfiles(ctx, db.ListMatchProfilesParams{
		UserIds: storekit.UUIDList(userIDs), Mode: modeDuel, SeasonID: seasonID,
		DefaultMmr: initialMMR, DefaultRd: initialRatingRD,
	})
	if err != nil {
		return nil, err
	}
	out := make(map[string]gameplay.PlayerProfile, len(userIDs))
	for _, userID := range userIDs {
		out[userID] = gameplay.PlayerProfile{UserID: userID, DisplayName: userID, MMR: initialMMR, RatingRD: initialRatingRD}
	}
	for _, row := range rows {
		userID := storekit.UUIDVal(row.UserID)
		out[userID] = gameplay.PlayerProfile{
			UserID: userID, DisplayName: row.DisplayName, AvatarURL: row.AvatarUrl,
			MMR: int(row.Mmr), RatingRD: row.RatingRd, RankedGamesPlayed: int(row.RankedGamesPlayed),
			IsGuest: row.IsGuest, IsAdmin: row.IsAdmin,
			SelectedBadge: matchBadge(badgekit.FromParts(row.BadgeCode, row.BadgeLevel, row.BadgeExtra, true)),
		}
	}
	return out, nil
}

// matchBadge is the badge a player shows in a match, as it is when the match starts; none when they
// show none.
func matchBadge(b badgekit.Badge) *gameplay.Badge {
	if b.ID == "" {
		return nil
	}
	return &gameplay.Badge{
		ID: b.ID, Kind: b.Kind, Label: b.Label, Description: b.Description, ImageURL: b.ImageURL, Rarity: b.Rarity,
		Level: b.Level, MaxLevel: b.MaxLevel, Extra: b.Extra, Owned: b.Owned, Unobtainable: b.Unobtainable,
	}
}
