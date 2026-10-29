-- name: CreateRoom :one
INSERT INTO rooms (kind, slug, name, description, password_hash)
VALUES (@kind, NULLIF(@slug::text, ''), @name, @description, NULLIF(@password_hash::text, ''))
RETURNING id;

-- name: ListRooms :many
SELECT * FROM rooms ORDER BY id;

-- name: RoomByID :one
SELECT * FROM rooms WHERE id = @id;

-- name: RoomBySlug :one
SELECT * FROM rooms WHERE slug = @slug::text;

-- name: SetRoomSlug :execrows
UPDATE rooms SET slug = NULLIF(@slug::text, '') WHERE id = @id;

-- name: SetRoomPassword :execrows
UPDATE rooms SET password_hash = NULLIF(@password_hash::text, '') WHERE id = @id;

-- name: DeleteRoomTicketsExceptBot :exec
-- 没有 bot 成员的房间 bot_member_id 为 NULL，写成 <> 就一张票据都不删。
DELETE FROM room_tickets
WHERE room_id = @room_id AND member_id IS DISTINCT FROM (SELECT bot_member_id FROM rooms WHERE id = @room_id);
