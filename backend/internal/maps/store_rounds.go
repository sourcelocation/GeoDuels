package maps

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"strings"

	"geoduels/internal/storekit"
	"geoduels/pkg/gameplay"
	"geoduels/pkg/matchkind"
	db "geoduels/pkg/persistence/sqlc/db"
)

// A match plays the map its config names, or the ruleset's map from the gameplay map settings when it
// names none. The matchmaker checks a chosen map before it starts the match (CheckMatchMap); the node
// that picks the match up picks its rounds (PickRounds), which nothing stores: a match lives on its
// node, and its replay records every round it played.

var (
	errMapUnavailable = errors.New("selected map unavailable")
	errMapNotReady    = errors.New("selected map is not ready")
	errMapPrivate     = errors.New("selected map is not accessible")
	errMapTooSmall    = errors.New("selected map has too few locations")
)

// roundsFor is how many rounds a match in this mode picks: a duel plays until someone runs out of
// health, a free-for-all and singleplayer a few rounds each.
func roundsFor(mode matchkind.Mode) int {
	if mode == matchkind.ModeFreeForAll || mode == matchkind.ModeSingleplayer {
		return minMapLocations
	}
	return plannedRoundCount
}

// CheckMatchMap checks that a match in this mode may play the map: it exists and is ready, the player
// whose private maps it may play can see it, and it has enough locations. It returns the map's id.
// The error says what's wrong, for the player.
func (s *PGStore) CheckMatchMap(ctx context.Context, mode matchkind.Mode, mapID, accessUserID string) (string, error) {
	row, err := s.playableMap(ctx, mapID, mode)
	if err != nil {
		return "", err
	}
	if !selectedMapAccessible(storekit.UUIDVal(row.OwnerUserID), accessUserID, string(row.Visibility)) {
		return "", errMapPrivate
	}
	return storekit.UUIDVal(row.ID), nil
}

// PickRounds picks the locations a new match plays, on the map its config names or the ruleset's.
// It returns the config the match plays, naming the map.
func (s *PGStore) PickRounds(ctx context.Context, matchID string, mode matchkind.Mode, cfg gameplay.MatchConfig) (gameplay.MatchConfig, []gameplay.LocationPoint, error) {
	cfg = gameplay.NormalizeMatchConfig(cfg)
	mapID := strings.TrimSpace(cfg.MapID)
	if mapID == "" {
		mapID = s.GetGameplayMapSettings().RulesetMap(cfg.Ruleset)
	}
	row, err := s.playableMap(ctx, mapID, mode)
	if err != nil {
		return cfg, nil, err
	}
	cfg.MapID, cfg.MapName = storekit.UUIDVal(row.ID), row.DisplayName
	rounds, err := s.pickLocations(ctx, cfg.MapID, deterministicPivot(matchID, cfg.MapID), roundsFor(mode))
	if err != nil {
		return cfg, nil, err
	}
	if len(rounds) < roundsFor(mode) {
		return cfg, nil, errMapTooSmall
	}
	return cfg, rounds, nil
}

// playableMap reads a map by its id or alias and checks it can be played in this mode.
func (s *PGStore) playableMap(ctx context.Context, mapID string, mode matchkind.Mode) (db.PlayableMapRow, error) {
	canonical, _, err := resolveMapIdentity(ctx, s.pool, strings.TrimSpace(mapID))
	if err != nil {
		return db.PlayableMapRow{}, errMapUnavailable
	}
	id, err := storekit.ProfileUUID(canonical)
	if err != nil {
		return db.PlayableMapRow{}, errMapUnavailable
	}
	row, err := s.q().PlayableMap(ctx, id)
	if err != nil {
		return db.PlayableMapRow{}, errMapUnavailable
	}
	if string(row.Status) != "ready" {
		return db.PlayableMapRow{}, errMapNotReady
	}
	if int(row.LocationCount) < roundsFor(mode) {
		return db.PlayableMapRow{}, errMapTooSmall
	}
	return row, nil
}

func selectedMapAccessible(ownerUserID, accessUserID, visibility string) bool {
	if ownerUserID == "" || ownerUserID == accessUserID {
		return true
	}
	switch strings.TrimSpace(strings.ToLower(visibility)) {
	case "public", "unlisted":
		return true
	default:
		return false
	}
}

// pickLocations takes limit of the map's locations in their random order from pivot on, continuing
// from the start when it runs past the end.
func (s *PGStore) pickLocations(ctx context.Context, mapID string, pivot int32, limit int) ([]gameplay.LocationPoint, error) {
	id, err := storekit.ProfileUUID(mapID)
	if err != nil {
		return nil, err
	}
	from, err := s.q().PickLocationsFrom(ctx, db.PickLocationsFromParams{MapID: id, MinRandKey: pivot, RowLimit: int32(limit)})
	if err != nil {
		return nil, err
	}
	out := make([]gameplay.LocationPoint, 0, limit)
	for _, row := range from {
		out = append(out, locationPoint(row.LatE7, row.LngE7, row.Country, row.PanoID.String, row.PanoID.Valid, row.HeadingCdeg.Int16, row.PitchCdeg.Int16))
	}
	if len(out) == limit {
		return out, nil
	}
	before, err := s.q().PickLocationsBefore(ctx, db.PickLocationsBeforeParams{MapID: id, MaxRandKey: pivot, RowLimit: int32(limit - len(out))})
	if err != nil {
		return nil, err
	}
	for _, row := range before {
		out = append(out, locationPoint(row.LatE7, row.LngE7, row.Country, row.PanoID.String, row.PanoID.Valid, row.HeadingCdeg.Int16, row.PitchCdeg.Int16))
	}
	return out, nil
}

func locationPoint(latE7, lngE7 int32, country, panoID string, hasPano bool, headingCdeg, pitchCdeg int16) gameplay.LocationPoint {
	point := gameplay.LocationPoint{Lat: float64(latE7) / 1e7, Lng: float64(lngE7) / 1e7, Country: country}
	if hasPano {
		point.PanoID = &panoID
	}
	heading, pitch := float64(headingCdeg)/100, float64(pitchCdeg)/100
	point.Heading, point.Pitch = &heading, &pitch
	return point
}

// deterministicPivot is where a match's pick starts in the map's random order, the same for a match
// whenever it's picked up.
func deterministicPivot(matchID, mapID string) int32 {
	sum := sha256.Sum256([]byte(matchID + ":" + mapID))
	return int32(binary.BigEndian.Uint32(sum[:4]) >> 8)
}
