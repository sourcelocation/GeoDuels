-- name: IncrementMapComment :exec
update maps set comment_count=comment_count+1, updated_at=now() where id=sqlc.arg(map_id)::uuid;

-- name: IncrementMapFavorite :exec
update maps set favorite_count=favorite_count+1, updated_at=now() where id=sqlc.arg(map_id)::uuid;

-- name: RefreshMapTrending :execrows
-- Sets every map's trending score from its last week: 3 for each day a player played it, 8 for each
-- favorite and 2 for each day a player commented on it. A map published within the week with at least
-- three of those scores up to 20 times as much, fading to once by the week's end.
WITH activity AS (
    SELECT h.map_id, 3 AS weight, count(DISTINCT (mp.user_id, h.ended_at::date)) AS events
    FROM match_history h JOIN match_players mp ON mp.match_id = h.match_id
    WHERE h.ended_at > now() - interval '7 days' AND h.map_id IS NOT NULL
    GROUP BY h.map_id
    UNION ALL
    SELECT f.map_id, 8, count(*) FROM map_favorites f
    WHERE f.created_at > now() - interval '7 days'
    GROUP BY f.map_id
    UNION ALL
    SELECT c.map_id, 2, count(DISTINCT (c.user_id, c.created_at::date)) FROM map_comments c
    WHERE c.created_at > now() - interval '7 days' AND c.deleted_at IS NULL
    GROUP BY c.map_id
), totals AS (
    SELECT map_id, sum(weight * events)::float8 AS weighted, sum(events)::bigint AS events
    FROM activity GROUP BY map_id
), scores AS (
    SELECT m.id, (coalesce(t.weighted, 0) * CASE
        WHEN coalesce(t.events, 0) >= 3 AND m.published_at IS NOT NULL
        THEN 1 + 19 * (1 - least(greatest(extract(epoch FROM now() - m.published_at)::float8 / 604800, 0), 1))
        ELSE 1 END)::float8 AS score
    FROM maps m LEFT JOIN totals t ON t.map_id = m.id
)
UPDATE maps m SET trending_score = s.score
FROM scores s
WHERE m.id = s.id AND m.trending_score IS DISTINCT FROM s.score;

-- name: RefreshMapPlays :execrows
-- Recounts, from match history, the plays of every map played in the last week.
UPDATE maps m SET play_count = c.plays
FROM (
    SELECT h.map_id, count(*)::int AS plays FROM match_history h
    WHERE h.map_id IN (
        SELECT DISTINCT recent.map_id FROM match_history recent
        WHERE recent.ended_at > now() - interval '7 days' AND recent.map_id IS NOT NULL
    )
    GROUP BY h.map_id
) c
WHERE m.id = c.map_id AND m.play_count IS DISTINCT FROM c.plays;
