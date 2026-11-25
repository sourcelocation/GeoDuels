package main

import (
	"context"
	"net/http"
	"time"

	"github.com/labstack/echo/v4"

	"geoduels/internal/matches"
	"geoduels/internal/parties"
	"geoduels/pkg/gameplay"
	"geoduels/pkg/matchkind"
)

// A match has one address, /api/v2/matches/{id}: its view says what it is and whether it is being
// played, and a seated player connects to it at /live/matches/{id}, which the gateway forwards to
// the gameplay node running it.

const (
	// placementWait is how long a connect waits for a node to pick the match up.
	placementWait = 10 * time.Second
	placementPoll = 150 * time.Millisecond
)

// matchViewStatus is what a viewer finds at a match's address.
type matchViewStatus string

const (
	matchViewStarting    matchViewStatus = "starting"
	matchViewLive        matchViewStatus = "live"
	matchViewEnded       matchViewStatus = "ended"
	matchViewInterrupted matchViewStatus = "interrupted"
	matchViewMissing     matchViewStatus = "missing"
	matchViewForbidden   matchViewStatus = "forbidden"
)

func matchViewStatusOf(status matches.SessionStatus) matchViewStatus {
	switch status {
	case matches.SessionStarting:
		return matchViewStarting
	case matches.SessionLive:
		return matchViewLive
	case matches.SessionEnded:
		return matchViewEnded
	case matches.SessionInterrupted:
		return matchViewInterrupted
	default:
		return matchViewMissing
	}
}

// matchView is everything a client needs about a match: what it is, whether it is being played and
// whether the viewer plays in it, where to go after it, and how it ended. A seated viewer of a
// starting or live match connects to /live/matches/{id}.
type matchView struct {
	MatchID string                      `json:"matchId"`
	Kind    matchkind.Kind              `json:"kind,omitempty"`
	Mode    matchkind.Mode              `json:"mode,omitempty"`
	Status  matchViewStatus             `json:"status"`
	Config  *gameplay.ClientMatchConfig `json:"config,omitempty"`
	Players []matchViewSeat             `json:"players,omitempty"`
	// Playing is set when the viewer has a seat in the match.
	Playing bool `json:"playing,omitempty"`
	// SignInRequired is set when an anonymous viewer finds a match being played.
	SignInRequired bool `json:"signInRequired,omitempty"`
	// CurrentMatchID is the viewer's own open match when it is another one.
	CurrentMatchID string             `json:"currentMatchId,omitempty"`
	Party          *matchViewParty    `json:"party,omitempty"`
	ReturnTarget   *matchReturnTarget `json:"returnTarget,omitempty"`
	// Result is the final snapshot of a match that ended and was recorded.
	Result *gameplay.ClientMatchResult `json:"result,omitempty"`
}

type matchViewSeat struct {
	UserID      string `json:"userId"`
	DisplayName string `json:"displayName"`
	AvatarURL   string `json:"avatarUrl,omitempty"`
	TeamID      string `json:"teamId,omitempty"`
}

type matchViewParty struct {
	ID         string `json:"id"`
	InviteCode string `json:"inviteCode,omitempty"`
}

// matchReturnTarget is where a client goes after a match, and where a player asks to go.
type matchReturnTarget struct {
	Kind            string `json:"kind"`
	MapID           string `json:"mapId,omitempty"`
	PartyID         string `json:"partyId,omitempty"`
	PartyInviteCode string `json:"partyInviteCode,omitempty"`
}

func newMatchReturnTarget(t *matches.ReturnTarget) *matchReturnTarget {
	if t == nil {
		return nil
	}
	return &matchReturnTarget{Kind: string(t.Kind), MapID: t.MapID, PartyID: t.PartyID, PartyInviteCode: t.PartyInviteCode}
}

// target is the target a client asked for, not yet normalized.
func (t *matchReturnTarget) target() *matches.ReturnTarget {
	if t == nil {
		return nil
	}
	return &matches.ReturnTarget{Kind: matches.ReturnKind(t.Kind), MapID: t.MapID, PartyID: t.PartyID, PartyInviteCode: t.PartyInviteCode}
}

// matchBootstrapResponse is a match page's view together with the session it bootstraps.
type matchBootstrapResponse struct {
	Auth  authSessionPayload `json:"auth"`
	Match matchView          `json:"match"`
}

// activeMatchSummary names the open match a player can return to.
type activeMatchSummary struct {
	MatchID string          `json:"matchId"`
	Kind    matchkind.Kind  `json:"kind"`
	Mode    matchkind.Mode  `json:"mode"`
	Status  matchViewStatus `json:"status"`
}

// viewMatch is what the viewer (empty when anonymous) sees of a match.
func (a *api) viewMatch(ctx context.Context, viewerID, matchID string) (matchView, error) {
	view := matchView{MatchID: matchID, Status: matchViewMissing}
	session, live, err := a.matchStore.GetSession(ctx, matchID)
	if err != nil {
		return view, err
	}
	result, recorded, err := a.publicMatchResult(matchID)
	if err != nil {
		return view, err
	}
	switch {
	case live:
		view.Kind, view.Status = session.Kind, matchViewStatusOf(session.Status)
	case recorded:
		view.Kind, view.Status = result.Kind, matchViewEnded
	default:
		return view, nil
	}
	view.Mode = matchkind.Of(view.Kind).Mode
	open := view.Status == matchViewStarting || view.Status == matchViewLive
	if viewerID == "" && open {
		view.SignInRequired = true
		return view, nil
	}
	if view.Status == matchViewEnded && recorded {
		view.Result = result
	}
	if live {
		config := gameplay.NewClientMatchConfig(session.Config)
		view.Config = &config
		for _, seat := range session.Seats {
			view.Players = append(view.Players, matchViewSeat{UserID: seat.UserID, DisplayName: seat.DisplayName, AvatarURL: seat.AvatarURL, TeamID: seat.Team})
		}
		view.Playing = open && session.Seated(viewerID)
		target, party := a.returnTarget(viewerID, session)
		view.ReturnTarget = newMatchReturnTarget(target)
		if party != nil {
			view.Party = &matchViewParty{ID: party.ID, InviteCode: party.InviteCode}
		}
	} else {
		config := result.Config
		view.Config = &config
		view.ReturnTarget = newMatchReturnTarget(&matches.ReturnTarget{Kind: matches.ReturnHome})
	}
	if viewerID != "" {
		if current, ok, err := a.matchStore.ActiveMatchForUser(ctx, viewerID); err == nil && ok && current.MatchID != matchID {
			view.CurrentMatchID = current.MatchID
		}
	}
	return view, nil
}

// returnTarget is where the viewer goes after the match: back to its party while they are still in
// it, otherwise where the match was started from.
func (a *api) returnTarget(viewerID string, session matches.Session) (*matches.ReturnTarget, *parties.Party) {
	target := matches.NormalizeReturnTarget(session.ReturnTarget)
	if target.Kind != matches.ReturnParty {
		return target, nil
	}
	home := &matches.ReturnTarget{Kind: matches.ReturnHome}
	if viewerID == "" || target.PartyID == "" {
		return home, nil
	}
	party, found, err := a.parties.GetPartyByID(target.PartyID)
	if err != nil || !found || party.State == parties.StateClosed || party.State == parties.StateExpired || !partyHasMember(party, viewerID) {
		return home, nil
	}
	target.PartyInviteCode = party.InviteCode
	return target, &party
}

func (a *api) getMatchView(c echo.Context) error {
	matchID := a.resolveEntityID("match", c.Param("id"))
	if matchID == "" {
		return plainTextError(c, http.StatusBadRequest, "invalid match")
	}
	viewerID := ""
	if claims, ok := a.optionalAuthenticatedClaims(c.Request()); ok {
		if banned, err := a.accountBanned(claims.Sub); err == nil && banned {
			return writeJSON(c, matchView{MatchID: matchID, Status: matchViewForbidden})
		}
		viewerID = claims.Sub
	}
	view, err := a.viewMatch(c.Request().Context(), viewerID, matchID)
	if err != nil {
		return plainTextError(c, http.StatusBadGateway, "match unavailable")
	}
	return writeJSON(c, view)
}

// matchBootstrap is a match page's first request: it renews the session from the refresh cookie
// and returns the view the signed-in player sees.
func (a *api) matchBootstrap(c echo.Context) error {
	matchID := a.resolveEntityID("match", c.Param("id"))
	if matchID == "" {
		return plainTextError(c, http.StatusBadRequest, "invalid match")
	}
	authPayload, nextRefreshToken, err := a.rotateSessionFromCookie(c.Request())
	if err != nil {
		a.clearRefreshCookie(c, c.Request())
		return plainTextError(c, http.StatusUnauthorized, "unauthorized")
	}
	a.setRefreshCookie(c, c.Request(), nextRefreshToken)
	banned, err := a.accountBanned(authPayload.User.ID)
	if err != nil {
		return plainTextError(c, http.StatusUnauthorized, "identity not found")
	}
	if banned {
		return writeJSON(c, matchBootstrapResponse{Auth: authPayload, Match: matchView{MatchID: matchID, Status: matchViewForbidden}})
	}
	view, err := a.viewMatch(c.Request().Context(), authPayload.User.ID, matchID)
	if err != nil {
		return plainTextError(c, http.StatusBadGateway, "match unavailable")
	}
	return writeJSON(c, matchBootstrapResponse{Auth: authPayload, Match: view})
}

// myMatch returns the match the player can return to, if any.
func (a *api) myMatch(c echo.Context) error {
	claims, err := a.authenticatedClaims(c.Request())
	if err != nil {
		return plainTextError(c, http.StatusUnauthorized, "unauthorized")
	}
	return writeJSON(c, map[string]any{"match": a.activeMatch(c.Request().Context(), claims.Sub)})
}

func (a *api) activeMatch(ctx context.Context, userID string) *activeMatchSummary {
	current, ok, err := a.matchStore.ActiveMatchForUser(ctx, userID)
	if err != nil || !ok {
		return nil
	}
	return &activeMatchSummary{MatchID: current.MatchID, Kind: current.Kind, Mode: matchkind.Of(current.Kind).Mode, Status: matchViewStatusOf(current.Status)}
}

// awaitPlacement waits until a node picks the match up or the wait runs out.
func (a *api) awaitPlacement(ctx context.Context, matchID string) matches.SessionStatus {
	deadline := time.Now().Add(placementWait)
	for {
		status, err := a.matchStore.MatchSessionStatus(ctx, matchID)
		if err == nil && status != matches.SessionStarting {
			return status
		}
		if time.Now().After(deadline) || ctx.Err() != nil {
			return status
		}
		time.Sleep(placementPoll)
	}
}

// matchNode finds the node running a player's match, waiting a moment if it's just starting. When it
// can't be played it returns the status to answer with.
func (a *api) matchNode(ctx context.Context, matchID, userID string) (bool, string, int) {
	session, found, err := a.matchStore.GetSession(ctx, matchID)
	if err != nil {
		return false, "", http.StatusBadGateway
	}
	if !found {
		return false, "", http.StatusNotFound
	}
	if !session.Seated(userID) {
		return false, "", http.StatusForbidden
	}
	if session.Status == matches.SessionStarting {
		a.awaitPlacement(ctx, matchID)
		if session, found, err = a.matchStore.GetSession(ctx, matchID); err != nil || !found {
			return false, "", http.StatusBadGateway
		}
	}
	if session.Status != matches.SessionLive || session.NodeURL == "" {
		return false, "", http.StatusGone
	}
	return true, session.NodeURL, http.StatusOK
}
