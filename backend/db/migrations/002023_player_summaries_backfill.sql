-- +goose Up
-- Fills the new summaries from history. From here on they are counted where matches are recorded.
--
-- Matches the previous release records while it is replaced count toward games and wins but not
-- toward flawless wins, perfect guesses or streaks, so the release that drops ranked_stats counts
-- again with gd_count_player_summaries. Streaks are counted for the active season only: past
-- seasons' are never shown.

-- +goose StatementBegin
CREATE FUNCTION public.gd_count_player_summaries() RETURNS void
LANGUAGE sql AS
$$
-- All-time duel totals. A flawless win ends with a duel's starting 6000 HP; a perfect guess scores 5000.
WITH duels AS (
    SELECT mp.user_id,
           count(*)::integer AS games_played,
           count(*) FILTER (WHERE h.winner_user_id = mp.user_id)::integer AS wins,
           count(*) FILTER (WHERE h.winner_user_id = mp.user_id AND mp.hp >= 6000)::integer AS flawless_wins
    FROM public.match_players mp
    JOIN public.match_history h ON h.match_id = mp.match_id
    WHERE h.mode = 'duel'
    GROUP BY mp.user_id
), perfect AS (
    SELECT user_id, count(*)::integer AS perfect_guesses
    FROM public.ranked_guess_events
    WHERE score = 5000
    GROUP BY user_id
)
INSERT INTO public.user_stats AS stats (user_id, games_played, wins, flawless_wins, perfect_guesses)
SELECT u.id, coalesce(d.games_played, 0), coalesce(d.wins, 0), coalesce(d.flawless_wins, 0), coalesce(p.perfect_guesses, 0)
FROM duels d
FULL JOIN perfect p ON p.user_id = d.user_id
JOIN public.users u ON u.id = coalesce(d.user_id, p.user_id)
ON CONFLICT (user_id) DO UPDATE SET
    games_played = greatest(stats.games_played, excluded.games_played),
    wins = greatest(stats.wins, excluded.wins),
    flawless_wins = excluded.flawless_wins,
    perfect_guesses = excluded.perfect_guesses;

-- Win streaks in the active season's ranked duels.
WITH season AS (
    SELECT value_json->>'activeSeasonId' AS season_id, (value_json->>'lastResetAt')::timestamptz AS started_at
    FROM public.site_settings
    WHERE key = 'ranked_season'
), played AS (
    SELECT mp.user_id, h.ended_at, h.match_id, h.winner_user_id = mp.user_id AS won
    FROM season
    JOIN public.match_history h ON h.ended_at >= season.started_at
    JOIN public.match_players mp ON mp.match_id = h.match_id
    WHERE h.mode = 'duel' AND h.ranked
), runs AS (
    -- A run is the wins after a loss; draws (won is null) belong to the run they fall in.
    SELECT user_id, won, count(*) FILTER (WHERE NOT won) OVER (PARTITION BY user_id ORDER BY ended_at, match_id) AS run
    FROM played
), streaks AS (
    SELECT user_id, max(wins)::integer AS best_streak, (array_agg(wins ORDER BY run DESC))[1]::integer AS current_streak
    FROM (SELECT user_id, run, count(*) FILTER (WHERE won) AS wins FROM runs GROUP BY user_id, run) per_run
    GROUP BY user_id
)
UPDATE public.ranks r
SET current_streak = s.current_streak, best_streak = s.best_streak
FROM streaks s, season
WHERE r.user_id = s.user_id AND r.mode = 'duel' AND r.season_id = season.season_id;
$$;
-- +goose StatementEnd

SELECT public.gd_count_player_summaries();

-- Every season's leaderboard as it stands now; the leaderboard job keeps the active one.
UPDATE public.ranks r
SET leaderboard_rank = s.position
FROM public.leaderboard_standings s
WHERE s.user_id = r.user_id AND s.mode = r.mode AND s.season_id = r.season_id;

-- +goose Down
-- The counts stay; 002022's Down removes their columns.
UPDATE public.ranks SET leaderboard_rank = NULL WHERE leaderboard_rank IS NOT NULL;
DROP FUNCTION public.gd_count_player_summaries();
