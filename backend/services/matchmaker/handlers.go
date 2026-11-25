package main

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"slices"
	"strings"

	"github.com/labstack/echo/v4"

	"geoduels/internal/httpx"
	"geoduels/internal/matches"
	"geoduels/internal/parties"
	"geoduels/internal/presence"
	"geoduels/internal/queue"
	"geoduels/internal/storekit"
	"geoduels/pkg/auth"
	"geoduels/pkg/gameplay"
	"geoduels/pkg/maintenance"
	"geoduels/pkg/matchkind"
)

// The matchmaker answers players directly, under /matchmaking. Every request is answered in its
// response; only what someone didn't ask for in that request (an opponent found while they wait, or
// what they did in another tab) reaches them as an event.

func routes(m *matchmaker) *echo.Echo {
	e := echo.New()
	e.HideBanner = true
	e.HidePort = true
	e.Use(httpx.CORS)
	e.GET("/health", m.healthReady)
	e.GET("/health/live", m.healthLive)
	e.GET("/health/ready", m.healthReady)
	e.GET("/matchmaking/queues", m.listQueues)
	e.POST("/matchmaking/queue", m.joinQueue)
	e.DELETE("/matchmaking/queue", m.leaveQueue)
	e.POST("/matchmaking/matches", m.startSolo)
	e.POST("/matchmaking/parties/:id/start", m.startParty)
	return e
}

func (m *matchmaker) healthLive(c echo.Context) error {
	return c.String(http.StatusOK, "ok")
}

func (m *matchmaker) healthReady(c echo.Context) error {
	if m.stopping.Load() {
		return c.String(http.StatusServiceUnavailable, "stopping")
	}
	if err := m.pool.Ping(c.Request().Context()); err != nil {
		return c.String(http.StatusServiceUnavailable, "database not ready")
	}
	return c.String(http.StatusOK, "ready")
}

// player is who sent the request, from their access token.
func (m *matchmaker) player(c echo.Context) (string, bool) {
	header := c.Request().Header.Get("Authorization")
	token, ok := strings.CutPrefix(header, "Bearer ")
	if !ok {
		return "", false
	}
	claims, err := auth.ValidateAppAccessToken(m.secret, strings.TrimSpace(token))
	if err != nil || claims.Sub == "" {
		return "", false
	}
	return claims.Sub, true
}

func writeRefusal(c echo.Context, refused refusal) error {
	body := map[string]string{"error": refused.Message, "message": refused.Message, "code": refused.Code}
	if refused.MatchID != "" {
		body["matchId"] = refused.MatchID
	}
	return c.JSON(refused.Status, body)
}

func decode(c echo.Context, into any) error {
	err := json.NewDecoder(http.MaxBytesReader(c.Response(), c.Request().Body, 64<<10)).Decode(into)
	if errors.Is(err, io.EOF) {
		return nil
	}
	return err
}

// queueView is a ranked queue as players see it.
type queueView struct {
	ID     string                     `json:"id"`
	Title  string                     `json:"title"`
	Detail string                     `json:"detail,omitempty"`
	Config gameplay.ClientMatchConfig `json:"config"`
}

func (m *matchmaker) listQueues(c echo.Context) error {
	offered := queue.Offered(m.settings)
	views := make([]queueView, len(offered))
	for i, q := range offered {
		views[i] = queueView{ID: q.ID, Title: q.Title, Detail: q.Detail, Config: gameplay.NewClientMatchConfig(q.Config)}
	}
	return c.JSON(http.StatusOK, map[string]any{"queues": views})
}

// queueReply answers a queue request: where the player is in the queue, or the match they were
// paired into, or the one they're still in.
type queueReply struct {
	Queue *queueState  `json:"queue,omitempty"`
	Match *activeMatch `json:"match,omitempty"`
}

type joinQueueRequest struct {
	Queues []string `json:"queues"`
}

// joinQueue queues the player and pairs the queue at once, so a player with an opponent waiting is
// answered with their match.
func (m *matchmaker) joinQueue(c echo.Context) error {
	userID, ok := m.player(c)
	if !ok {
		return c.String(http.StatusUnauthorized, "unauthorized")
	}
	ctx := c.Request().Context()
	if status := maintenance.Current(m.settings); status.QueueBlocked() {
		return c.String(http.StatusServiceUnavailable, maintenanceQueueMessage(status))
	}
	var req joinQueueRequest
	if err := decode(c, &req); err != nil {
		return c.String(http.StatusBadRequest, "invalid payload")
	}
	chosen := queue.Offered(m.settings).Choose(req.Queues)
	if len(chosen) == 0 {
		return c.String(http.StatusBadRequest, "choose a queue")
	}
	if _, refused, err := m.seatsFor(ctx, m.pool, matchkind.Of(matchkind.RankedDuel), []matchkind.Seat{{UserID: userID}}); err != nil {
		return c.String(http.StatusBadGateway, "queue unavailable")
	} else if refused.refused() {
		return c.String(refused.Status, refused.Message)
	}
	// A player already in a match that queueing would not replace goes back to it.
	if current, ok, err := m.matches.ActiveMatchForUser(ctx, userID); err == nil && ok && !matchkind.Of(current.Kind).Replaceable {
		match := activeMatch{MatchID: current.MatchID, Kind: current.Kind, Mode: matchkind.Of(current.Kind).Mode, Status: string(current.Status)}
		return c.JSON(http.StatusOK, queueReply{Match: &match})
	}
	joinedAt, err := m.queue.Join(ctx, userID, chosen, storekit.CurrentSeasonID(m.settings))
	if err != nil {
		return c.String(http.StatusBadGateway, "queue unavailable")
	}
	queued := queueState{Queued: true, QueuedAt: joinedAt.UnixMilli()}
	m.tell([]string{userID}, queued)
	matches, err := m.pass(ctx)
	if err == nil {
		for _, match := range matches {
			if slices.Contains(match.Players, userID) {
				starting := startingMatch(match.MatchID, match.Kind)
				return c.JSON(http.StatusOK, queueReply{Match: &starting})
			}
		}
	}
	return c.JSON(http.StatusOK, queueReply{Queue: &queued})
}

func (m *matchmaker) leaveQueue(c echo.Context) error {
	userID, ok := m.player(c)
	if !ok {
		return c.String(http.StatusUnauthorized, "unauthorized")
	}
	if _, err := m.queue.Leave(c.Request().Context(), userID); err != nil {
		return c.String(http.StatusBadGateway, "queue unavailable")
	}
	left := queueState{}
	m.tell([]string{userID}, left)
	return c.JSON(http.StatusOK, queueReply{Queue: &left})
}

// soloRequest starts a match the player plays alone.
type soloRequest struct {
	Kind         matchkind.Kind             `json:"kind"`
	Config       gameplay.ClientMatchConfig `json:"config"`
	ReturnTarget *returnTarget              `json:"returnTarget,omitempty"`
}

// startedView is a match just started, before a node has picked it up.
type startedView struct {
	activeMatch
	ReturnTarget returnTarget `json:"returnTarget"`
}

func (m *matchmaker) startSolo(c echo.Context) error {
	userID, ok := m.player(c)
	if !ok {
		return c.String(http.StatusUnauthorized, "unauthorized")
	}
	var req soloRequest
	if err := decode(c, &req); err != nil {
		return c.String(http.StatusBadRequest, "invalid payload")
	}
	spec := matchkind.Of(req.Kind)
	if spec.Origin != matchkind.OriginDirect {
		return c.String(http.StatusBadRequest, "this kind of match cannot be started directly")
	}
	config, refused, err := m.chosenMap(c, spec, gameplay.NormalizeMatchConfig(req.Config.MatchConfig()), userID)
	if err != nil || refused.refused() {
		return m.answerStart(c, started{}, refused, err)
	}
	target := matches.NormalizeReturnTarget(req.ReturnTarget.target())
	if target.Kind == matches.ReturnParty {
		target = &matches.ReturnTarget{Kind: matches.ReturnHome}
	}
	match, refused, err := m.start(c.Request().Context(), startRequest{
		Kind: req.Kind, Seats: []matchkind.Seat{{UserID: userID}}, Config: config, ReturnTarget: target,
	})
	return m.answerStart(c, match, refused, err)
}

// startParty starts the party's match with every member seated, once they are all connected.
func (m *matchmaker) startParty(c echo.Context) error {
	userID, ok := m.player(c)
	if !ok {
		return c.String(http.StatusUnauthorized, "unauthorized")
	}
	ctx := c.Request().Context()
	party, found, err := m.parties.GetPartyByID(strings.TrimSpace(c.Param("id")))
	if err != nil {
		return c.String(http.StatusBadGateway, "party unavailable")
	}
	if !found {
		return c.String(http.StatusNotFound, "party not found")
	}
	if party.OwnerUserID != userID {
		return c.String(http.StatusForbidden, "forbidden")
	}
	if party.State != parties.StateOpen {
		return c.String(http.StatusConflict, "party is not open")
	}
	kind, ok := matchkind.ForParty(party.Mode)
	if !ok {
		return c.String(http.StatusConflict, "unsupported party mode")
	}
	spec := matchkind.Of(kind)
	memberIDs := make([]string, len(party.Members))
	for i, member := range party.Members {
		memberIDs[i] = member.UserID
	}
	online, err := presence.Online(ctx, m.pool, memberIDs)
	if err != nil {
		return c.String(http.StatusBadGateway, "party unavailable")
	}
	var missing []string
	seats := make([]matchkind.Seat, 0, len(party.Members))
	for _, member := range party.Members {
		if !online[member.UserID] {
			missing = append(missing, memberName(member))
		}
		seat := matchkind.Seat{UserID: member.UserID}
		if spec.Teams {
			seat.Team = partyTeam(member.TeamID)
		}
		seats = append(seats, seat)
	}
	if len(missing) > 0 {
		return c.String(http.StatusConflict, "all players must be connected to start: "+strings.Join(missing, ", "))
	}
	if err := spec.CheckSeats(seats); err != nil {
		return c.String(http.StatusConflict, err.Error())
	}
	config, refused, err := m.chosenMap(c, spec, party.Config, party.OwnerUserID)
	if err != nil || refused.refused() {
		return m.answerStart(c, started{}, refused, err)
	}
	match, refused, err := m.start(ctx, startRequest{
		Kind: kind, Seats: seats, Config: config, PartyID: party.ID,
		ReturnTarget: &matches.ReturnTarget{Kind: matches.ReturnParty, PartyID: party.ID},
	})
	return m.answerStart(c, match, refused, err)
}

// chosenMap checks the map a match chose, as the player whose private maps it may play; a match that
// names no map plays the ruleset's, which the node resolves.
func (m *matchmaker) chosenMap(c echo.Context, spec matchkind.Spec, config gameplay.MatchConfig, accessUserID string) (gameplay.MatchConfig, refusal, error) {
	if spec.Maps != matchkind.MapsChosen || strings.TrimSpace(config.MapID) == "" {
		config.MapID = ""
		return config, refusal{}, nil
	}
	mapID, err := m.maps.CheckMatchMap(c.Request().Context(), spec.Mode, config.MapID, accessUserID)
	if err != nil {
		return config, refusal{Status: http.StatusConflict, Code: "map_unavailable", Message: err.Error()}, nil
	}
	config.MapID = mapID
	return config, refusal{}, nil
}

func (m *matchmaker) answerStart(c echo.Context, match started, refused refusal, err error) error {
	if err != nil {
		c.Logger().Errorf("match start failed: %v", err)
		return c.String(http.StatusBadGateway, "match could not start")
	}
	if refused.refused() {
		return writeRefusal(c, refused)
	}
	return c.JSON(http.StatusCreated, startedView{activeMatch: startingMatch(match.MatchID, match.Kind), ReturnTarget: newReturnTarget(match.ReturnTarget)})
}

func memberName(member parties.Member) string {
	if name := strings.TrimSpace(member.DisplayName); name != "" {
		return name
	}
	return member.UserID
}

func partyTeam(teamID string) string {
	if strings.ToLower(strings.TrimSpace(teamID)) == "b" {
		return "b"
	}
	return "a"
}
