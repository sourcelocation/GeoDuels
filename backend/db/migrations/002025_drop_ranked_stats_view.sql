-- +goose Up
-- Every pod runs the release that made ranks a player's season, so nothing reads ranked_stats.
-- Matches the release before it recorded during that rollout missed their flawless wins, perfect
-- guesses and streaks: count them again, holding new results back while counting.
SET LOCAL lock_timeout = '5s';

DROP VIEW public.ranked_stats;

LOCK TABLE public.ranks, public.user_stats IN SHARE ROW EXCLUSIVE MODE;
SELECT public.gd_count_player_summaries();

-- +goose Down
CREATE VIEW public.ranked_stats AS
    SELECT user_id, mode, season_id, games_played, wins, updated_at FROM public.ranks;
