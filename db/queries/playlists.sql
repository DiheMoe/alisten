-- name: CreateFavorites :exec
INSERT INTO playlists (owner_id, kind, name) VALUES (@owner_id, 'favorites', '我喜欢');

-- name: MemberPlaylists :many
SELECT p.id, p.kind, p.name, p.created_at, p.updated_at, count(i.track_id)::bigint AS track_count
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

-- name: SetPlaylistSeq :exec
UPDATE playlists SET item_seq = @item_seq, updated_at = now() WHERE id = @id;

-- name: PlaylistItems :many
SELECT * FROM playlist_items WHERE playlist_id = @playlist_id ORDER BY position DESC;

-- name: DeletePlaylistItem :exec
WITH deleted AS (
    DELETE FROM playlist_items WHERE playlist_id = @playlist_id AND source = @source AND track_id = @track_id
    RETURNING playlist_id
)
UPDATE playlists SET updated_at = now() WHERE id IN (SELECT playlist_id FROM deleted);
