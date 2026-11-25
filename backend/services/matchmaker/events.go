package main

import (
	"geoduels/internal/matches"
	"geoduels/pkg/matchkind"
)

// What the matchmaker tells people, over their live connection or in a response. These are the API's
// queue.updated and match.started events, shared through tests/shared/live-events.json, which
// events_test.go checks this side against.

// activeMatch names an open match a player can go to.
type activeMatch struct {
	MatchID string         `json:"matchId"`
	Kind    matchkind.Kind `json:"kind"`
	Mode    matchkind.Mode `json:"mode"`
	Status  string         `json:"status"`
}

// startingMatch is a match just written, before a node has picked it up.
func startingMatch(matchID string, kind matchkind.Kind) activeMatch {
	return activeMatch{MatchID: matchID, Kind: kind, Mode: matchkind.Of(kind).Mode, Status: string(matches.SessionStarting)}
}

// queueState is where a person is in the queue.
type queueState struct {
	Queued   bool   `json:"queued"`
	QueuedAt int64  `json:"queuedAt,omitempty"`
	Error    string `json:"error,omitempty"`
}

func (queueState) EventType() string { return "queue.updated" }

// matchStarted tells a player they're seated in a new match.
type matchStarted struct {
	Match activeMatch `json:"match"`
}

func (matchStarted) EventType() string { return "match.started" }

// returnTarget is where a client goes after a match.
type returnTarget struct {
	Kind    string `json:"kind"`
	MapID   string `json:"mapId,omitempty"`
	PartyID string `json:"partyId,omitempty"`
}

func newReturnTarget(t *matches.ReturnTarget) returnTarget {
	return returnTarget{Kind: string(t.Kind), MapID: t.MapID, PartyID: t.PartyID}
}

// target is the target a client asked for, not yet normalized.
func (t *returnTarget) target() *matches.ReturnTarget {
	if t == nil {
		return nil
	}
	return &matches.ReturnTarget{Kind: matches.ReturnKind(t.Kind), MapID: t.MapID, PartyID: t.PartyID}
}
