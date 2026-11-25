-- +goose Up
-- The matchmaker creates every match and the gameplay node picks its rounds, so nothing plans rounds
-- ahead; map stats are counted from match history by a periodic job; a party's match is the newest
-- one started from it; Map of the Week nominations stay until they win; and the ranked queues are a
-- setting. This release needs a maintenance window: the previous release's pods stop first.

DROP TABLE public.match_round_plans;
DROP TABLE public.map_daily_users;
DROP TABLE public.map_stats_daily;

-- One row per player, with every queue they wait in. Losing it in a crash only means queueing again.
DROP TABLE public.queue_tickets;
CREATE UNLOGGED TABLE public.queue_tickets (
    user_id uuid PRIMARY KEY REFERENCES public.users(id) ON DELETE CASCADE,
    queues text[] NOT NULL CHECK (cardinality(queues) > 0),
    mmr integer NOT NULL,
    rd double precision NOT NULL,
    -- Who the player last played in a rated match, whom they don't meet again straight away.
    last_opponent_id uuid,
    joined_at timestamp with time zone DEFAULT now() NOT NULL
);

DROP INDEX public.idx_parties_last_match;
ALTER TABLE public.parties DROP COLUMN last_match_id;
CREATE INDEX match_sessions_party ON public.match_sessions USING btree (source_party_id, created_at DESC)
    WHERE source_party_id IS NOT NULL;

DELETE FROM public.motw_nominations n
USING public.motw_schedule s
WHERE n.cycle_start <> s.starts_at;
ALTER TABLE public.motw_nominations DROP COLUMN cycle_start;
ALTER TABLE public.motw_nominations ADD CONSTRAINT motw_nominations_map_id_key UNIQUE (map_id);

INSERT INTO public.site_settings (key, value_json, updated_at)
VALUES ('ranked_queues', '{"queues": [
    {"id": "moving", "title": "Moving", "detail": "Street names",
     "config": {"ruleset": "moving", "streetNames": "shown", "multiplierMode": "individual"}},
    {"id": "no_move_hidden", "title": "No Move", "detail": "No street names",
     "config": {"ruleset": "no_move", "streetNames": "hidden", "multiplierMode": "individual"}}
]}'::jsonb, now())
ON CONFLICT (key) DO NOTHING;

-- +goose Down
-- Brings the tables back empty: the planned rounds, daily map stats, queued players, each party's
-- last match and Map of the Week's earlier nominations are not restored.
DELETE FROM public.site_settings WHERE key = 'ranked_queues';

ALTER TABLE public.motw_nominations DROP CONSTRAINT motw_nominations_map_id_key;
ALTER TABLE public.motw_nominations ADD COLUMN cycle_start timestamp with time zone;
UPDATE public.motw_nominations SET cycle_start = (SELECT starts_at FROM public.motw_schedule WHERE singleton);
ALTER TABLE public.motw_nominations ALTER COLUMN cycle_start SET NOT NULL;
ALTER TABLE public.motw_nominations ADD CONSTRAINT motw_nominations_cycle_start_map_id_key UNIQUE (cycle_start, map_id);

DROP INDEX public.match_sessions_party;
ALTER TABLE public.parties ADD COLUMN last_match_id uuid;
CREATE INDEX idx_parties_last_match ON public.parties USING btree (last_match_id);

DROP TABLE public.queue_tickets;
CREATE UNLOGGED TABLE public.queue_tickets (
    user_id uuid NOT NULL REFERENCES public.users(id) ON DELETE CASCADE,
    variant text NOT NULL,
    mmr integer NOT NULL,
    joined_at timestamp with time zone DEFAULT now() NOT NULL,
    seen_at timestamp with time zone DEFAULT now() NOT NULL,
    PRIMARY KEY (user_id, variant)
);

CREATE TABLE public.map_stats_daily (
    day date NOT NULL,
    plays integer DEFAULT 0 NOT NULL,
    favorites integer DEFAULT 0 NOT NULL,
    comments integer DEFAULT 0 NOT NULL,
    unique_players integer DEFAULT 0 NOT NULL,
    unique_favoriters integer DEFAULT 0 NOT NULL,
    unique_commenters integer DEFAULT 0 NOT NULL,
    map_id uuid NOT NULL REFERENCES public.maps(id) ON DELETE CASCADE,
    PRIMARY KEY (map_id, day)
);

CREATE TABLE public.map_daily_users (
    day date NOT NULL,
    user_id uuid NOT NULL REFERENCES public.users(id) ON DELETE CASCADE,
    played boolean DEFAULT false NOT NULL,
    favorited boolean DEFAULT false NOT NULL,
    commented boolean DEFAULT false NOT NULL,
    map_id uuid NOT NULL REFERENCES public.maps(id) ON DELETE CASCADE,
    PRIMARY KEY (map_id, day, user_id)
);

CREATE TABLE public.match_round_plans (
    match_id uuid NOT NULL,
    round_index integer NOT NULL CHECK (round_index >= 0),
    lat double precision NOT NULL,
    lng double precision NOT NULL,
    country text,
    pano_id text,
    heading double precision,
    pitch double precision,
    map_id uuid NOT NULL REFERENCES public.maps(id),
    PRIMARY KEY (match_id, round_index)
);
