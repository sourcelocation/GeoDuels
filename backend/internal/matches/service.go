package matches

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5"

	"geoduels/pkg/gameplay"
)

// Store is the match lifecycle and history: starting matches, the node side of running them,
// finalizing them, and reading them back.
type Store interface {
	StartTx(ctx context.Context, tx pgx.Tx, p StartParams) (Conflict, error)
	EndMatch(ctx context.Context, matchID, outcome string) (bool, error)
	PickUp(ctx context.Context, node Node, run func(PickedUp) (gameplay.MatchConfig, error)) (string, error)
	NodeOpenMatches(ctx context.Context, node Node) (map[string]bool, error)
	FinalizeMatch(snap gameplay.MatchSnapshot) (gameplay.MatchSnapshot, error)
	MatchSessionStatus(ctx context.Context, matchID string) (SessionStatus, error)
	GetSession(ctx context.Context, matchID string) (Session, bool, error)
	ActiveMatchForUser(ctx context.Context, userID string) (ActiveMatch, bool, error)
	UsersInLiveMatches(ctx context.Context, userIDs []string) (map[string]bool, error)
	FinalMatchSnapshot(matchID string) (*gameplay.MatchSnapshot, bool, error)
	ListPlayerMatchHistory(userID string, limit int) ([]MatchHistorySummary, error)
	ListPlayerMatchHistoryPage(userID string, limit int, beforeEndedAt time.Time, beforeMatchID string, rankedOnly bool) (MatchHistoryPage, error)
	PlayerParticipatedInMatch(userID, matchID string) (bool, error)
}

var _ Store = (*PGStore)(nil)
