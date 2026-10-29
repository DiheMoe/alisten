-- name: TakeRoom :one
UPDATE rooms SET term = term + 1 WHERE id = @id RETURNING *;

-- name: RoomEntries :many
SELECT e.entry_id, e.slot, e.source, e.track_id, e.title, e.artist, e.album, e.cover_url, e.cover_key, e.page_url, e.meta_duration_ms,
       e.by_member_id, e.by_name, e.by_avatar_hash, e.auto, e.likes, e.round, e.added_at, e.started_at,
       t.title AS track_title, t.artist AS track_artist, t.album AS track_album, t.cover_url AS track_cover_url,
       t.cover_key AS track_cover_key, t.page_url AS track_page_url, t.duration_ms, t.gain, t.lyric
FROM room_entries e
LEFT JOIN tracks t ON t.source = e.source AND t.track_id = e.track_id
WHERE e.room_id = @room_id
ORDER BY e.entry_id;

-- name: SaveRoomState :execrows
UPDATE rooms SET name = @name, description = @description, play_mode = @play_mode, entry_seq = @entry_seq
WHERE id = @id AND term = @term;

-- name: DeleteRoomEntries :exec
DELETE FROM room_entries WHERE room_id = @room_id;

-- name: InsertRoomEntries :copyfrom
INSERT INTO room_entries (
    room_id, entry_id, slot, source, track_id, title, artist, album, cover_url, cover_key, page_url, meta_duration_ms,
    by_member_id, by_name, by_avatar_hash, auto, likes, round, added_at, started_at
) VALUES (
    @room_id, @entry_id, @slot, @source, @track_id, @title, @artist, @album, @cover_url, @cover_key, @page_url, @meta_duration_ms,
    @by_member_id, @by_name, @by_avatar_hash, @auto, @likes, @round, @added_at, @started_at
);

-- name: InsertPlays :copyfrom
INSERT INTO plays (room_id, source, track_id, by_member_id, by_name, by_avatar_hash, auto, started_at, ended_at)
VALUES (@room_id, @source, @track_id, @by_member_id, @by_name, @by_avatar_hash, @auto, @started_at, @ended_at);

-- name: MarkPlayed :exec
-- 不同房间同时落盘时，先开播的那一次可能后写入。
UPDATE tracks SET last_played_at = GREATEST(last_played_at, @started_at::timestamptz)
WHERE source = @source AND track_id = @track_id;

-- name: DeleteRoom :execrows
DELETE FROM rooms WHERE id = @id AND term = @term;
