-- +goose Up
-- A player's inbox lists newest first; only unread messages were indexed by player.
CREATE INDEX idx_inbox_messages_user ON public.inbox_messages USING btree (user_id, id DESC);

-- +goose Down
DROP INDEX public.idx_inbox_messages_user;
