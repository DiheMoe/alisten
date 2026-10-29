-- name: ListStoredMedia :many
SELECT media_key::text AS media_key, source, track_id, title, media_size::bigint AS media_size, cover_key, prepared_at, last_played_at FROM tracks
WHERE media_key IS NOT NULL
ORDER BY prepared_at DESC, track_id;

-- name: StoredMedia :one
SELECT media_key::text AS media_key, source, track_id, title, media_size::bigint AS media_size, cover_key, prepared_at, last_played_at FROM tracks
WHERE media_key = @media_key::text;

-- name: DeleteStoredMedia :exec
DELETE FROM tracks WHERE media_key = @media_key::text;
