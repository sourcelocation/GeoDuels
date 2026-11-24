-- name: EnsureRankedSeasonSettings :exec
INSERT INTO site_settings(key, value_json, updated_at)
VALUES ('ranked_season', convert_from($1, 'UTF8')::jsonb, now())
ON CONFLICT (key) DO NOTHING;

-- name: GetActiveSeasonID :one
SELECT (value_json->>'activeSeasonId')::text FROM site_settings WHERE key = 'ranked_season';

-- Who is on a season's leaderboard, and in what order, is leaderboard_standings. Pages and profiles
-- read the places it gave at the last refresh, kept on ranks.

-- name: GetLeaderboardTotals :one
-- A season's leaderboard has as many players as its last place.
SELECT coalesce((SELECT r.leaderboard_rank FROM ranks r
                 WHERE r.user_id = nullif(sqlc.arg(self_user_id), '')::uuid
                   AND r.mode = sqlc.arg(mode) AND r.season_id = sqlc.arg(season_id)), 0)::int AS self_rank,
       coalesce((SELECT max(r.leaderboard_rank) FROM ranks r
                 WHERE r.mode = sqlc.arg(mode) AND r.season_id = sqlc.arg(season_id)
                   AND r.leaderboard_rank IS NOT NULL), 0)::int AS total_players;

-- name: GetRankedSeasonSettings :one
SELECT value_json
FROM site_settings
WHERE key = 'ranked_season';

-- name: GetRankedSeasonSettingsForUpdate :one
SELECT value_json
FROM site_settings
WHERE key = 'ranked_season'
FOR UPDATE;

-- name: ListLeaderboard :many
SELECT r.leaderboard_rank::int AS rank,
       r.user_id AS user_id,
       coalesce(nullif(u.display_name, r.user_id::text), ui.provider_name, r.user_id::text) AS display_name,
       coalesce(u.avatar_url, ui.avatar_url, '') AS avatar_url,
       r.mmr,
       r.games_played,
       r.wins
FROM ranks r
JOIN users u ON u.id = r.user_id
LEFT JOIN LATERAL (
  SELECT provider_name, avatar_url FROM user_identities
  WHERE user_id = r.user_id AND provider = 'google'
  ORDER BY created_at ASC LIMIT 1
) ui ON true
WHERE r.mode = sqlc.arg(mode) AND r.season_id = sqlc.arg(season_id)
  AND r.leaderboard_rank > sqlc.arg(row_offset)::int
  AND r.leaderboard_rank <= sqlc.arg(row_offset)::int + sqlc.arg(row_limit)::int
ORDER BY r.leaderboard_rank;

-- name: ListRankedSeasonFinishers :many
SELECT user_id FROM leaderboard_standings
WHERE mode = $1 AND season_id = $2 AND position <= 100
ORDER BY position;

-- name: RefreshLeaderboard :execrows
-- Moves the season's players to the places the standings give them. A row a match is updating right
-- now is skipped rather than waited for; it moves on the next refresh.
WITH standings AS (
    SELECT s.user_id, s.position FROM leaderboard_standings s
    WHERE s.mode = sqlc.arg(mode) AND s.season_id = sqlc.arg(season_id)
), moved AS (
    SELECT r.user_id, standings.position
    FROM ranks r
    LEFT JOIN standings ON standings.user_id = r.user_id
    WHERE r.mode = sqlc.arg(mode) AND r.season_id = sqlc.arg(season_id)
      AND r.leaderboard_rank IS DISTINCT FROM standings.position
    FOR UPDATE OF r SKIP LOCKED
)
UPDATE ranks r
SET leaderboard_rank = moved.position
FROM moved
WHERE r.mode = sqlc.arg(mode) AND r.season_id = sqlc.arg(season_id) AND r.user_id = moved.user_id;

-- name: SeedRankedSeasonRanks :execrows
INSERT INTO ranks(user_id, mode, season_id, mmr, rd)
SELECT u.id, $1, $2, $3, $4
FROM users u
WHERE gd_is_registered(u.id)
ON CONFLICT (user_id, mode, season_id) DO NOTHING;

-- name: WriteRankedSeasonSettings :exec
INSERT INTO site_settings(key, value_json, updated_at)
VALUES ('ranked_season', convert_from($1, 'UTF8')::jsonb, now())
ON CONFLICT (key) DO UPDATE SET value_json = excluded.value_json, updated_at = now();
