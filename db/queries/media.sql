-- name: ListStoredMedia :many
-- 曲目 ID 按字节比较，排序不随数据库的 locale 变化。
SELECT media_key::text AS media_key, source, track_id, title, artist, media_size::bigint AS media_size, cover_url, cover_key, prepared_at, last_played_at FROM tracks
WHERE media_key IS NOT NULL AND title ILIKE '%' || @pattern::text || '%' ESCAPE '\'
ORDER BY CASE WHEN @sort::text = 'stored' THEN prepared_at END DESC,
         CASE WHEN @sort::text = 'idle' THEN last_played_at END ASC NULLS FIRST,
         CASE WHEN @sort::text = 'size' THEN media_size END DESC,
         track_id COLLATE "C"
LIMIT @page_size::bigint OFFSET @skip::bigint;

-- name: CountStoredMedia :one
SELECT count(*) AS total, coalesce(sum(media_size), 0)::bigint AS total_bytes FROM tracks
WHERE media_key IS NOT NULL AND title ILIKE '%' || @pattern::text || '%' ESCAPE '\';

-- name: StoredMedia :one
SELECT media_key::text AS media_key, source, track_id, title, artist, media_size::bigint AS media_size, cover_url, cover_key, prepared_at, last_played_at FROM tracks
WHERE media_key = @media_key::text;

-- name: DeleteStoredMedia :exec
DELETE FROM tracks WHERE media_key = @media_key::text;
