package maps

import (
	"context"
	"encoding/json"
	"strings"
	"time"

	"geoduels/pkg/gameplay"
	"geoduels/pkg/matchkind"
)

// Gameplay map configuration: which map answers each mode/ruleset slot.
// The maps feature owns catalog CRUD; this stays with persistence because
// match coordination, parties, and match planning resolve maps at runtime.

const gameplayMapSettingsKey = "gameplay_map_settings"

func defaultGameplayMapSettings() GameplaySettings {
	return GameplaySettings{
		MovingMapID: MapKeyMoving,
		NoMoveMapID: MapKeyNMPZ,
		NMPZMapID:   MapKeyNMPZ,
	}
}

type legacyGameplayMapSettings struct {
	MovingMapID             string `json:"movingMapId"`
	NoMoveMapID             string `json:"noMoveMapId"`
	NMPZMapID               string `json:"nmpzMapId"`
	RankedMovingMapID       string `json:"rankedMovingMapId"`
	RankedNMPZMapID         string `json:"rankedNmpzMapId"`
	SingleplayerMovingMapID string `json:"singleplayerMovingMapId"`
	SingleplayerNMPZMapID   string `json:"singleplayerNmpzMapId"`
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if trimmed := strings.TrimSpace(value); trimmed != "" {
			return trimmed
		}
	}
	return ""
}

func normalizeGameplayMapSettings(settings GameplaySettings) GameplaySettings {
	defaults := defaultGameplayMapSettings()
	if strings.TrimSpace(settings.MovingMapID) == "" {
		settings.MovingMapID = defaults.MovingMapID
	}
	if strings.TrimSpace(settings.NoMoveMapID) == "" {
		settings.NoMoveMapID = defaults.NoMoveMapID
	}
	if strings.TrimSpace(settings.NMPZMapID) == "" {
		settings.NMPZMapID = defaults.NMPZMapID
	}
	return settings
}

func decodeGameplayMapSettings(raw string) GameplaySettings {
	var legacy legacyGameplayMapSettings
	if err := json.Unmarshal([]byte(raw), &legacy); err != nil {
		return defaultGameplayMapSettings()
	}
	return normalizeGameplayMapSettings(GameplaySettings{
		MovingMapID: firstNonEmpty(legacy.MovingMapID, legacy.RankedMovingMapID, legacy.SingleplayerMovingMapID),
		// Ranked NMPZ became ranked No Move; keep that map for the no-move slot.
		NoMoveMapID: firstNonEmpty(legacy.NoMoveMapID, legacy.RankedNMPZMapID),
		NMPZMapID:   firstNonEmpty(legacy.NMPZMapID, legacy.SingleplayerNMPZMapID, legacy.RankedNMPZMapID),
	})
}

// Settings is a process's copy of the site settings (sitesettings.Copy).
type Settings interface {
	Value(key string) ([]byte, bool)
}

// GetGameplayMapSettings is the gameplay map settings in the process's copy of the settings.
func (s *PGStore) GetGameplayMapSettings() GameplaySettings {
	raw, ok := s.settings.Value(gameplayMapSettingsKey)
	if !ok {
		return defaultGameplayMapSettings()
	}
	return decodeGameplayMapSettings(string(raw))
}

// RulesetMap is the map a match plays in this ruleset when it names none.
func (g GameplaySettings) RulesetMap(ruleset gameplay.Ruleset) string {
	switch gameplay.NormalizeRuleset(ruleset) {
	case gameplay.RulesetNoMove:
		return g.NoMoveMapID
	case gameplay.RulesetNMPZ:
		return g.NMPZMapID
	default:
		return g.MovingMapID
	}
}

// ResolveGameplayMapID is the id of the requested map, or of the ruleset's map when none was requested.
func (s *PGStore) ResolveGameplayMapID(_ matchkind.Mode, ruleset gameplay.Ruleset, requestedMapID string) (string, error) {
	requestedMapID = strings.TrimSpace(requestedMapID)
	if requestedMapID == "" {
		requestedMapID = s.GetGameplayMapSettings().RulesetMap(ruleset)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
	defer cancel()
	id, _, err := resolveMapIdentity(ctx, s.pool, requestedMapID)
	return id, err
}
