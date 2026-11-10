-- name: CreateFavorites :exec
INSERT INTO playlists (owner_id, kind, name) VALUES (@owner_id, 'favorites', '我喜欢');

-- name: MemberPlaylists :many
SELECT p.id, p.kind, p.name, p.created_at, p.updated_at, p.share_id, count(i.track_id)::bigint AS track_count
FROM playlists p
LEFT JOIN playlist_items i ON i.playlist_id = p.id
WHERE p.owner_id = @owner_id
GROUP BY p.id
ORDER BY p.kind = 'favorites' DESC, p.created_at, p.id;

-- name: MemberPlaylistCovers :many
SELECT DISTINCT ON (i.playlist_id) i.playlist_id, i.cover_url, i.cover_key
FROM playlist_items i
JOIN playlists p ON p.id = i.playlist_id
WHERE p.owner_id = @owner_id AND (i.cover_url IS NOT NULL OR i.cover_key IS NOT NULL)
ORDER BY i.playlist_id, i.position DESC;

-- name: LockFavorites :exec
-- 新建歌单前锁住这个账号的“我喜欢”：同一个账号并发新建时依次计数，不会越过上限。
SELECT id FROM playlists WHERE owner_id = @owner_id AND kind = 'favorites' FOR UPDATE;

-- name: CountPlaylists :one
SELECT count(*) FROM playlists WHERE owner_id = @owner_id;

-- name: CreatePlaylist :one
INSERT INTO playlists (owner_id, kind, name) VALUES (@owner_id, 'custom', @name)
RETURNING id, created_at, updated_at;

-- name: PlaylistKind :one
SELECT kind FROM playlists WHERE id = @id AND owner_id = @owner_id;

-- name: RenamePlaylist :execrows
UPDATE playlists SET name = @name, updated_at = now() WHERE id = @id AND owner_id = @owner_id AND kind = 'custom';

-- name: DeletePlaylist :execrows
DELETE FROM playlists WHERE id = @id AND owner_id = @owner_id AND kind = 'custom';

-- name: LockPlaylist :one
-- 改动曲目前锁住歌单：并发加入时依次检查上限、分配排序值。删除歌单先锁歌单行再级联删除曲目，其他操作也要先锁歌单再动曲目，否则会互相死锁。
SELECT item_seq FROM playlists WHERE id = @id AND owner_id = @owner_id FOR UPDATE;

-- name: PlaylistHasItem :one
SELECT EXISTS (SELECT FROM playlist_items WHERE playlist_id = @playlist_id AND source = @source AND track_id = @track_id);

-- name: CountPlaylistItems :one
SELECT count(*) FROM playlist_items WHERE playlist_id = @playlist_id;

-- name: InsertPlaylistItem :exec
INSERT INTO playlist_items (playlist_id, source, track_id, title, artist, album, cover_url, cover_key, duration_ms, page_url, position)
VALUES (@playlist_id, @source, @track_id, @title, @artist, @album, @cover_url, @cover_key, @duration_ms, @page_url, @position);

-- name: InsertPlaylistItems :copyfrom
INSERT INTO playlist_items (playlist_id, source, track_id, title, artist, album, cover_url, cover_key, duration_ms, page_url, position)
VALUES (@playlist_id, @source, @track_id, @title, @artist, @album, @cover_url, @cover_key, @duration_ms, @page_url, @position);

-- name: SetPlaylistSeq :exec
UPDATE playlists SET item_seq = @item_seq, updated_at = now() WHERE id = @id;

-- name: PlaylistItems :many
SELECT * FROM playlist_items WHERE playlist_id = @playlist_id ORDER BY position DESC;

-- name: DeletePlaylistItems :exec
WITH deleted AS (
    DELETE FROM playlist_items i
    USING unnest(@sources::text[]) WITH ORDINALITY AS s(source, ord)
    JOIN unnest(@track_ids::text[]) WITH ORDINALITY AS t(track_id, ord) USING (ord)
    WHERE i.playlist_id = @playlist_id AND i.source = s.source AND i.track_id = t.track_id
    RETURNING i.playlist_id
)
UPDATE playlists SET updated_at = now() WHERE id IN (SELECT playlist_id FROM deleted);

-- name: CopyPlaylistItems :execrows
-- 按请求的先后复制：第一首的排序值最大。请求里重复的歌只取第一次出现的位置。
WITH wanted AS (
    SELECT s.source, k.track_id, min(s.ord) AS ord
    FROM unnest(@sources::text[]) WITH ORDINALITY AS s(source, ord)
    JOIN unnest(@track_ids::text[]) WITH ORDINALITY AS k(track_id, ord) USING (ord)
    GROUP BY s.source, k.track_id
), copied AS (
    SELECT i.source, i.track_id, i.title, i.artist, i.album, i.cover_url, i.cover_key, i.duration_ms, i.page_url,
           row_number() OVER (ORDER BY w.ord DESC) AS rank
    FROM wanted w
    JOIN playlist_items i ON i.playlist_id = @from_id AND i.source = w.source AND i.track_id = w.track_id
    WHERE NOT EXISTS (
        SELECT FROM playlist_items d WHERE d.playlist_id = @to_id AND d.source = w.source AND d.track_id = w.track_id
    )
)
INSERT INTO playlist_items (playlist_id, source, track_id, title, artist, album, cover_url, cover_key, duration_ms, page_url, position)
SELECT sqlc.arg(to_id), source, track_id, title, artist, album, cover_url, cover_key, duration_ms, page_url, sqlc.arg(item_seq)::bigint + rank
FROM copied;

-- name: PlaylistTracks :many
SELECT source, track_id FROM playlist_items WHERE playlist_id = @playlist_id ORDER BY position DESC;

-- name: SetPlaylistPositions :exec
UPDATE playlist_items i SET position = t.position
FROM unnest(@sources::text[]) WITH ORDINALITY AS s(source, ord)
JOIN unnest(@track_ids::text[]) WITH ORDINALITY AS k(track_id, ord) USING (ord)
JOIN unnest(@positions::bigint[]) WITH ORDINALITY AS t(position, ord) USING (ord)
WHERE i.playlist_id = @playlist_id AND i.source = s.source AND i.track_id = k.track_id;

-- name: TouchPlaylist :exec
UPDATE playlists SET updated_at = now() WHERE id = @id;

-- name: TrackPlaylists :many
SELECT p.id FROM playlists p
JOIN playlist_items i ON i.playlist_id = p.id
WHERE p.owner_id = @owner_id AND i.source = @source AND i.track_id = @track_id
ORDER BY p.id;

-- name: SetPlaylistShare :execrows
UPDATE playlists SET share_id = @share_id WHERE id = @id AND owner_id = @owner_id;

-- name: SharedPlaylist :one
SELECT p.id, p.kind, p.name, m.name AS owner_name, m.avatar_hash AS owner_avatar_hash
FROM playlists p
JOIN members m ON m.id = p.owner_id
WHERE p.share_id = sqlc.arg(share_id)::text;

-- name: CopySharedPlaylistItems :one
-- 确认分享还在和复制曲目在同一条语句里，看到的是同一个快照：要么完整复制，要么读不到分享。不锁来源歌单：两个人同时复制
-- 对方分享的“我喜欢”时，各自已经锁住了自己的“我喜欢”，再去锁对方的就会互相等待。
WITH shared AS (
    SELECT id FROM playlists WHERE share_id = sqlc.arg(share_id)::text
), copied AS (
    INSERT INTO playlist_items (playlist_id, source, track_id, title, artist, album, cover_url, cover_key, duration_ms, page_url, position)
    SELECT sqlc.arg(to_id), i.source, i.track_id, i.title, i.artist, i.album, i.cover_url, i.cover_key, i.duration_ms, i.page_url,
           row_number() OVER (ORDER BY i.position)
    FROM playlist_items i
    JOIN shared s ON s.id = i.playlist_id
    RETURNING 1
)
SELECT EXISTS (SELECT FROM shared) AS found, (SELECT count(*) FROM copied)::bigint AS copied;

-- name: PlaylistCover :one
SELECT cover_url, cover_key FROM playlist_items
WHERE playlist_id = @playlist_id AND (cover_url IS NOT NULL OR cover_key IS NOT NULL)
ORDER BY position DESC
LIMIT 1;
