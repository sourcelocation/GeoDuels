-- name: PlayableMap :one
-- A map as a match plays it: who may play it, whether it's ready, and how many locations it has.
select id, display_name, owner_user_id, visibility, status, location_count from maps
where id=sqlc.arg(map_id) and archived_at is null;

-- name: PickLocationsFrom :many
-- Up to row_limit of the map's locations, in their random order from min_rand_key on.
select lat_e7, lng_e7, coalesce(country,'') AS country, pano_id, heading_cdeg, pitch_cdeg from locations
where map_storage_id=(select storage_id from maps where id=sqlc.arg(map_id)) and rand_key_i >= sqlc.arg(min_rand_key)
order by rand_key_i asc limit sqlc.arg(row_limit);

-- name: PickLocationsBefore :many
-- Up to row_limit of the map's locations, in their random order before max_rand_key: where a pick
-- that ran past the end continues.
select lat_e7, lng_e7, coalesce(country,'') AS country, pano_id, heading_cdeg, pitch_cdeg from locations
where map_storage_id=(select storage_id from maps where id=sqlc.arg(map_id)) and rand_key_i < sqlc.arg(max_rand_key)
order by rand_key_i asc limit sqlc.arg(row_limit);
