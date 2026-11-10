-- name: LockListeners :many
-- 按 ID 的顺序锁住开启了记录的成员：同一个成员的历史依次写入，删掉超出上限的部分时不会多删或少删；
-- 加锁的顺序固定，同时写几个成员的事务之间不会死锁。
SELECT id FROM members WHERE id = ANY(@ids::uuid[]) AND record_listens ORDER BY id FOR NO KEY UPDATE;

-- name: UpsertListens :exec
INSERT INTO listens (member_id, source, track_id, title, artist, album, cover_url, cover_key, duration_ms, page_url, heard_at)
SELECT m, @source::text, @track_id::text, @title::text, sqlc.narg(artist)::text, sqlc.narg(album)::text, sqlc.narg(cover_url)::text,
       sqlc.narg(cover_key)::text, @duration_ms::bigint, @page_url::text, @heard_at::timestamptz
FROM unnest(@member_ids::uuid[]) AS m
ON CONFLICT (member_id, source, track_id) DO UPDATE
SET title = excluded.title, artist = excluded.artist, album = excluded.album, cover_url = excluded.cover_url,
    cover_key = excluded.cover_key, duration_ms = excluded.duration_ms, page_url = excluded.page_url,
    heard_at = greatest(listens.heard_at, excluded.heard_at);

-- name: MoveListens :exec
-- 并入的规则与 UpsertListens 相同，并入的那份算新写入的；to_id 关闭了记录时只删不并。
WITH moved AS (
    DELETE FROM listens WHERE member_id = sqlc.arg(from_id)::uuid
    RETURNING source, track_id, title, artist, album, cover_url, cover_key, duration_ms, page_url, heard_at
)
INSERT INTO listens (member_id, source, track_id, title, artist, album, cover_url, cover_key, duration_ms, page_url, heard_at)
SELECT sqlc.arg(to_id)::uuid, source, track_id, title, artist, album, cover_url, cover_key, duration_ms, page_url, heard_at
FROM moved
WHERE EXISTS (SELECT FROM members WHERE id = sqlc.arg(to_id)::uuid AND record_listens)
ON CONFLICT (member_id, source, track_id) DO UPDATE
SET title = excluded.title, artist = excluded.artist, album = excluded.album, cover_url = excluded.cover_url,
    cover_key = excluded.cover_key, duration_ms = excluded.duration_ms, page_url = excluded.page_url,
    heard_at = greatest(listens.heard_at, excluded.heard_at);

-- name: TrimListens :exec
-- 排序与 MemberListens 相同：留下的正是读取时返回的那些。
DELETE FROM listens l
USING (
    SELECT member_id, source, track_id,
           row_number() OVER (PARTITION BY member_id ORDER BY heard_at DESC, source, track_id) AS n
    FROM listens WHERE member_id = ANY(@member_ids::uuid[])
) ranked
WHERE l.member_id = ranked.member_id AND l.source = ranked.source AND l.track_id = ranked.track_id AND ranked.n > @keep::bigint;

-- name: MemberListens :many
SELECT source, track_id, title, artist, album, cover_url, cover_key, duration_ms, page_url, heard_at
FROM listens WHERE member_id = @member_id
ORDER BY heard_at DESC, source, track_id
LIMIT @max;

-- name: DeleteListen :exec
DELETE FROM listens WHERE member_id = @member_id AND source = @source AND track_id = @track_id;

-- name: ClearListens :exec
DELETE FROM listens WHERE member_id = @member_id;

-- name: ListenRecording :one
SELECT record_listens FROM members WHERE id = @id;

-- name: SetListenRecording :execrows
UPDATE members SET record_listens = @record_listens WHERE id = @id;
