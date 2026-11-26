package main

import (
	"context"
	"fmt"
	"net/http"
	"strings"

	"github.com/jackc/pgx/v5"

	"geoduels/internal/accounts"
	"geoduels/internal/matches"
	"geoduels/internal/queue"
	"geoduels/pkg/entityid"
	"geoduels/pkg/gameplay"
	"geoduels/pkg/maintenance"
	"geoduels/pkg/matchkind"
	db "geoduels/pkg/persistence/sqlc/db"
)

// Every match starts here, whether the queue paired its players, a party's owner started it, or a
// player started one alone: the players are taken out of the queue, checked, and seated with the
// match, all in one transaction. A gameplay node picks the match up from there.

// startRequest is a match to start.
type startRequest struct {
	Kind   matchkind.Kind
	Seats  []matchkind.Seat
	Config gameplay.MatchConfig
	// PartyID is the party the match was started from.
	PartyID      string
	ReturnTarget *matches.ReturnTarget
}

// started is a match that was written, and who plays it.
type started struct {
	MatchID      string
	Kind         matchkind.Kind
	Players      []string
	ReturnTarget *matches.ReturnTarget
}

// refusal is why a match can't start, as the player is told. The zero refusal is none. UserID is the
// player it's about; empty when it's about everyone, as during maintenance.
type refusal struct {
	UserID  string
	Status  int
	Code    string
	Message string
	// MatchID is the match the player is already in.
	MatchID string
}

func (r refusal) refused() bool { return r.Message != "" }

// startTx starts a match in tx: it takes the players out of the queue, checks they may play, and
// writes the match and its seats. Taking the tickets first means a start and a pairing pass lock a
// player in the same order. A refused start leaves tx for the caller to roll back.
func (m *matchmaker) startTx(ctx context.Context, tx pgx.Tx, req startRequest) (started, refusal, error) {
	spec, ok := matchkind.Lookup(req.Kind)
	if !ok {
		return started{}, refusal{}, fmt.Errorf("unknown match kind %q", req.Kind)
	}
	if err := spec.CheckSeats(req.Seats); err != nil {
		return started{}, refusal{}, err
	}
	userIDs := make([]string, len(req.Seats))
	for i, seat := range req.Seats {
		userIDs[i] = seat.UserID
	}
	if _, err := queue.TakeTx(ctx, tx, userIDs); err != nil {
		return started{}, refusal{}, err
	}
	seats, refused, err := m.seatsFor(ctx, tx, spec, req.Seats)
	if err != nil || refused.refused() {
		return started{}, refused, err
	}
	matchID := entityid.New()
	target := matches.NormalizeReturnTarget(req.ReturnTarget)
	conflict, err := m.matches.StartTx(ctx, tx, matches.StartParams{
		MatchID: matchID, Kind: req.Kind, Seats: seats, Config: req.Config, PartyID: req.PartyID, ReturnTarget: target,
	})
	if err != nil {
		return started{}, refusal{}, err
	}
	if conflict.Blocked() {
		return started{}, conflictRefusal(conflict, seats), nil
	}
	if req.PartyID != "" {
		if err := m.jobs.EnqueuePartyChanged(ctx, tx, req.PartyID); err != nil {
			return started{}, refusal{}, err
		}
	}
	if err := m.jobs.EnqueuePresenceChanged(ctx, tx, userIDs); err != nil {
		return started{}, refusal{}, err
	}
	return started{MatchID: matchID, Kind: req.Kind, Players: userIDs, ReturnTarget: target}, refusal{}, nil
}

// seatsFor checks that every player may play a match of this kind now, and gives their seats.
func (m *matchmaker) seatsFor(ctx context.Context, q db.DBTX, spec matchkind.Spec, requested []matchkind.Seat) ([]matches.Seat, refusal, error) {
	if status := maintenance.Current(m.settings); status.PlayBlocked() {
		return nil, refusal{Status: http.StatusServiceUnavailable, Code: "maintenance", Message: maintenancePlayMessage(status)}, nil
	}
	userIDs := make([]string, len(requested))
	for i, seat := range requested {
		userIDs[i] = seat.UserID
	}
	players, err := accounts.Players(ctx, q, userIDs)
	if err != nil {
		return nil, refusal{}, err
	}
	seats := make([]matches.Seat, 0, len(requested))
	for _, seat := range requested {
		player, ok := players[seat.UserID]
		if !ok {
			return nil, notAdmitted(seat.UserID, "", "account unavailable"), nil
		}
		switch {
		case player.Banned:
			return nil, notAdmitted(seat.UserID, player.DisplayName, "account is banned"), nil
		case player.NeedsNickname:
			return nil, notAdmitted(seat.UserID, player.DisplayName, "nickname required"), nil
		case !player.Registered && !spec.GuestsAllow:
			return nil, notAdmitted(seat.UserID, player.DisplayName, "account required"), nil
		}
		seats = append(seats, matches.Seat{UserID: seat.UserID, Team: seat.Team, DisplayName: seatName(player), AvatarURL: player.AvatarURL})
	}
	return seats, refusal{}, nil
}

func seatName(player accounts.Player) string {
	if name := strings.TrimSpace(player.DisplayName); name != "" {
		return name
	}
	return player.UserID
}

func notAdmitted(userID, name, reason string) refusal {
	message := reason
	if name != "" {
		message = name + ": " + reason
	}
	return refusal{UserID: userID, Status: http.StatusForbidden, Code: "not_admitted", Message: message}
}

// conflictRefusal is the refusal for a player already in a match.
func conflictRefusal(conflict matches.Conflict, seats []matches.Seat) refusal {
	name := conflict.UserID
	for _, seat := range seats {
		if seat.UserID == conflict.UserID {
			name = seat.DisplayName
		}
	}
	message := name + " is already in a match"
	if conflict.Kind != "" {
		message = name + " is already in a " + strings.ReplaceAll(string(conflict.Kind), "_", " ") + " match"
	}
	return refusal{UserID: conflict.UserID, Status: http.StatusConflict, Code: "ACTIVE_MATCH", Message: message, MatchID: conflict.MatchID}
}

// start starts a match in a transaction of its own and tells its players once it has committed.
func (m *matchmaker) start(ctx context.Context, req startRequest) (started, refusal, error) {
	tx, err := m.pool.Begin(ctx)
	if err != nil {
		return started{}, refusal{}, err
	}
	defer tx.Rollback(ctx)
	match, refused, err := m.startTx(ctx, tx, req)
	if err != nil || refused.refused() {
		return started{}, refused, err
	}
	if err := tx.Commit(ctx); err != nil {
		return started{}, refusal{}, err
	}
	m.tellStarted(match)
	return match, refusal{}, nil
}

func (m *matchmaker) tellStarted(match started) {
	m.tell(match.Players, matchStarted{Match: startingMatch(match.MatchID, match.Kind)})
}

func maintenancePlayMessage(status maintenance.Status) string {
	if status.Message != "" {
		return status.Message
	}
	switch status.Phase {
	case maintenance.PhaseActive:
		return "Maintenance in progress. New matches are temporarily unavailable."
	case maintenance.PhaseWarning:
		return "New matches have been paused for scheduled maintenance."
	default:
		return "Matches are temporarily unavailable"
	}
}

func maintenanceQueueMessage(status maintenance.Status) string {
	if status.Message != "" {
		return status.Message
	}
	switch status.Phase {
	case maintenance.PhaseActive:
		return "Maintenance in progress. Queueing is temporarily unavailable."
	case maintenance.PhaseWarning:
		return "Queueing has been paused for scheduled maintenance."
	default:
		return "Queue unavailable"
	}
}
