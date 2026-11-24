-- +goose Up
-- Profiles and leaderboards read summaries kept where the facts are written, never a player's history.
--
-- - ranks is a player's season: rating, ranked games and wins, win streaks, and their place on the
--   season's leaderboard, which the leaderboard job keeps once a minute. ranked_stats folds into it.
-- - user_stats is a player's all-time totals, now with flawless wins and perfect guesses.
-- - leaderboard_standings says who is on a season's leaderboard and in what order. Being registered
--   or banned is defined once, as gd_registered_users and gd_banned_users, which the gd_is_*
--   predicates now read.
--
-- No maintenance window: the previous release keeps running while this one rolls out, and still
-- reads and writes ranked_stats, so ranked_stats stays as a view over ranks until the next release.

SET LOCAL lock_timeout = '5s';

CREATE VIEW public.gd_registered_users AS
    SELECT DISTINCT i.user_id AS id FROM public.user_identities i;

CREATE VIEW public.gd_banned_users AS
    SELECT u.id FROM public.users u
    WHERE u.banned_at IS NOT NULL AND (u.ban_expires_at IS NULL OR u.ban_expires_at > now());

-- +goose StatementBegin
CREATE OR REPLACE FUNCTION public.gd_is_registered(p_user_id uuid) RETURNS boolean
LANGUAGE sql STABLE PARALLEL SAFE AS
$$ SELECT EXISTS (SELECT 1 FROM public.gd_registered_users r WHERE r.id = p_user_id) $$;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE OR REPLACE FUNCTION public.gd_is_guest(p_user_id uuid) RETURNS boolean
LANGUAGE sql STABLE PARALLEL SAFE AS
$$ SELECT NOT EXISTS (SELECT 1 FROM public.gd_registered_users r WHERE r.id = p_user_id) $$;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE OR REPLACE FUNCTION public.gd_is_banned(p_user_id uuid) RETURNS boolean
LANGUAGE sql STABLE PARALLEL SAFE AS
$$ SELECT EXISTS (SELECT 1 FROM public.gd_banned_users b WHERE b.id = p_user_id) $$;
-- +goose StatementEnd

ALTER TABLE public.ranks
    ADD COLUMN games_played integer DEFAULT 0 NOT NULL,
    ADD COLUMN wins integer DEFAULT 0 NOT NULL,
    -- Ranked wins since the last loss; draws neither extend nor break a streak.
    ADD COLUMN current_streak integer DEFAULT 0 NOT NULL,
    ADD COLUMN best_streak integer DEFAULT 0 NOT NULL,
    -- Place on the season's leaderboard as of the last refresh; null when not on it.
    ADD COLUMN leaderboard_rank integer;

CREATE INDEX idx_ranks_leaderboard ON public.ranks USING btree (mode, season_id, leaderboard_rank)
    WHERE leaderboard_rank IS NOT NULL;

ALTER TABLE public.user_stats
    ADD COLUMN flawless_wins integer DEFAULT 0 NOT NULL,
    ADD COLUMN perfect_guesses integer DEFAULT 0 NOT NULL;

CREATE VIEW public.leaderboard_standings AS
    SELECT r.mode, r.season_id, r.user_id,
           row_number() OVER (PARTITION BY r.mode, r.season_id ORDER BY r.mmr DESC, r.updated_at ASC, r.user_id ASC)::integer AS position
    FROM public.ranks r
    WHERE EXISTS (SELECT 1 FROM public.gd_registered_users g WHERE g.id = r.user_id)
      AND NOT EXISTS (SELECT 1 FROM public.gd_banned_users b WHERE b.id = r.user_id);

-- The previous release writes ranked_stats as well as ranks; hold its writes while they move over.
LOCK TABLE public.ranked_stats IN EXCLUSIVE MODE;

UPDATE public.ranks r
SET games_played = s.games_played, wins = s.wins
FROM public.ranked_stats s
WHERE s.user_id = r.user_id AND s.mode = r.mode AND s.season_id = r.season_id
  AND (s.games_played <> 0 OR s.wins <> 0);

INSERT INTO public.ranks (user_id, mode, season_id, games_played, wins)
SELECT s.user_id, s.mode, s.season_id, s.games_played, s.wins FROM public.ranked_stats s
ON CONFLICT (user_id, mode, season_id) DO NOTHING;

DROP TABLE public.ranked_stats;

-- For the previous release only, while it is replaced. The next release drops it.
CREATE VIEW public.ranked_stats AS
    SELECT user_id, mode, season_id, games_played, wins, updated_at FROM public.ranks;

-- +goose Down
-- ranked_stats.updated_at comes back as the rank's updated_at.
DROP VIEW public.ranked_stats;
CREATE TABLE public.ranked_stats (
    user_id uuid NOT NULL,
    mode public.gd_match_mode NOT NULL,
    season_id text NOT NULL,
    games_played integer DEFAULT 0 NOT NULL,
    wins integer DEFAULT 0 NOT NULL,
    updated_at timestamp with time zone DEFAULT now() NOT NULL
);
INSERT INTO public.ranked_stats (user_id, mode, season_id, games_played, wins, updated_at)
SELECT user_id, mode, season_id, games_played, wins, updated_at FROM public.ranks;
ALTER TABLE ONLY public.ranked_stats
    ADD CONSTRAINT ranked_stats_pkey PRIMARY KEY (user_id, mode, season_id);
ALTER TABLE ONLY public.ranked_stats
    ADD CONSTRAINT ranked_stats_user_id_fkey FOREIGN KEY (user_id) REFERENCES public.users(id);

DROP VIEW public.leaderboard_standings;
ALTER TABLE public.user_stats DROP COLUMN flawless_wins, DROP COLUMN perfect_guesses;
DROP INDEX public.idx_ranks_leaderboard;
ALTER TABLE public.ranks
    DROP COLUMN games_played,
    DROP COLUMN wins,
    DROP COLUMN current_streak,
    DROP COLUMN best_streak,
    DROP COLUMN leaderboard_rank;

-- +goose StatementBegin
CREATE OR REPLACE FUNCTION public.gd_is_registered(p_user_id uuid) RETURNS boolean
LANGUAGE sql STABLE PARALLEL SAFE AS
$$ SELECT EXISTS (SELECT 1 FROM public.user_identities i WHERE i.user_id = p_user_id) $$;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE OR REPLACE FUNCTION public.gd_is_guest(p_user_id uuid) RETURNS boolean
LANGUAGE sql STABLE PARALLEL SAFE AS
$$ SELECT NOT EXISTS (SELECT 1 FROM public.user_identities i WHERE i.user_id = p_user_id) $$;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE OR REPLACE FUNCTION public.gd_is_banned(p_user_id uuid) RETURNS boolean
LANGUAGE sql STABLE PARALLEL SAFE AS
$$ SELECT EXISTS (
     SELECT 1 FROM public.users u
     WHERE u.id = p_user_id
       AND u.banned_at IS NOT NULL
       AND (u.ban_expires_at IS NULL OR u.ban_expires_at > now())
   ) $$;
-- +goose StatementEnd

DROP VIEW public.gd_banned_users;
DROP VIEW public.gd_registered_users;
