package queue

import (
	"encoding/json"
	"strings"

	"geoduels/pkg/gameplay"
)

// The ranked queues are a setting: which matches are offered, what they're called and how they're
// played. The code knows what a ruleset and street names mean; which of them are offered is data,
// changed without a release.
const queuesKey = "ranked_queues"

// Queue is one ranked queue: a named match config. Without a map in its config it plays the
// ruleset's map from the gameplay map settings.
type Queue struct {
	ID     string               `json:"id"`
	Title  string               `json:"title"`
	Detail string               `json:"detail,omitempty"`
	Config gameplay.MatchConfig `json:"config"`
}

// Queues are the queues offered, in the order players see them.
type Queues []Queue

// Settings is a process's copy of the site settings (sitesettings.Copy).
type Settings interface {
	Value(key string) ([]byte, bool)
}

// Offered is the queues in the settings. A queue without an id, or with one already used, is left out.
func Offered(settings Settings) Queues {
	raw, ok := settings.Value(queuesKey)
	if !ok {
		return Queues{}
	}
	var stored struct {
		Queues []Queue `json:"queues"`
	}
	if json.Unmarshal(raw, &stored) != nil {
		return Queues{}
	}
	out := Queues{}
	seen := map[string]bool{}
	for _, q := range stored.Queues {
		q.ID = strings.TrimSpace(q.ID)
		if q.ID == "" || seen[q.ID] {
			continue
		}
		seen[q.ID] = true
		q.Config = gameplay.NormalizeMatchConfig(q.Config)
		out = append(out, q)
	}
	return out
}

// Get is the offered queue with this id.
func (qs Queues) Get(id string) (Queue, bool) {
	for _, q := range qs {
		if q.ID == id {
			return q, true
		}
	}
	return Queue{}, false
}

// Choose is the offered queues among these ids, in the offered order.
func (qs Queues) Choose(ids []string) []string {
	wanted := map[string]bool{}
	for _, id := range ids {
		wanted[strings.TrimSpace(id)] = true
	}
	out := []string{}
	for _, q := range qs {
		if wanted[q.ID] {
			out = append(out, q.ID)
		}
	}
	return out
}
