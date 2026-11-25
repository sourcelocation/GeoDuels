-- name: JoinQueue :one
-- Queues the player in these queues at their rating in the season, with their last rated opponent.
-- Queueing again changes the queues and keeps their place.
INSERT INTO queue_tickets (user_id, queues, mmr, rd, last_opponent_id)
SELECT p.user_id, sqlc.arg(queues)::text[], coalesce(r.mmr, sqlc.arg(default_mmr)::int),
       coalesce(r.rd, sqlc.arg(default_rd)::float8), last.opponent_id
FROM (SELECT sqlc.arg(user_id)::uuid AS user_id) p
LEFT JOIN ranks r ON r.user_id = p.user_id AND r.mode = 'duel' AND r.season_id = sqlc.arg(season_id)
LEFT JOIN LATERAL (
    SELECT other.user_id AS opponent_id
    FROM match_players mine
    JOIN match_history h ON h.match_id = mine.match_id AND h.ranked
    JOIN match_players other ON other.match_id = mine.match_id AND other.user_id <> mine.user_id
    WHERE mine.user_id = p.user_id
    ORDER BY mine.ended_at DESC
    LIMIT 1
) last ON true
ON CONFLICT (user_id) DO UPDATE SET queues = excluded.queues
RETURNING joined_at;

-- name: LeaveQueue :execrows
DELETE FROM queue_tickets WHERE user_id = $1;

-- name: TakeQueueTickets :many
-- Takes these players out of the queue, returning who was in it.
DELETE FROM queue_tickets WHERE user_id = ANY(sqlc.arg(user_ids)::uuid[])
RETURNING user_id;

-- name: QueueTicketJoinedAt :one
SELECT joined_at FROM queue_tickets WHERE user_id = $1;

-- name: LockWaitingTickets :many
-- The connected players waiting that no other pass holds, longest-waiting first, locked until this
-- pass ends.
SELECT t.user_id, t.queues, t.mmr, t.rd, t.last_opponent_id, t.joined_at
FROM queue_tickets t
WHERE EXISTS (SELECT 1 FROM live_connections c WHERE c.user_id = t.user_id)
ORDER BY t.joined_at, t.user_id
FOR UPDATE OF t SKIP LOCKED;

-- name: DropDisconnectedTickets :many
-- Takes out the players with no connection left. A gateway reports a closed connection only once a
-- reload would have reconnected, so a reload keeps the player's place.
DELETE FROM queue_tickets WHERE user_id IN (
    SELECT t.user_id FROM queue_tickets t
    WHERE NOT EXISTS (SELECT 1 FROM live_connections c WHERE c.user_id = t.user_id)
    FOR UPDATE OF t SKIP LOCKED
)
RETURNING user_id;
