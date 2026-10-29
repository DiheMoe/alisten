-- name: LockRoomPassword :one
-- FOR SHARE 让修改密码和删除房间等到这次签发提交之后，它们随后删除票据时会删掉这一张。
SELECT coalesce(password_hash, '')::text AS password_hash FROM rooms WHERE id = @id FOR SHARE;

-- name: CreateTicket :exec
INSERT INTO room_tickets (token_hash, room_id, member_id) VALUES (@token_hash, @room_id, @member_id);

-- name: TicketHolder :one
SELECT t.room_id, m.id, m.name, m.avatar_hash, r.bot_member_id IS NOT DISTINCT FROM m.id AS bot
FROM room_tickets t
JOIN members m ON m.id = t.member_id
JOIN rooms r ON r.id = t.room_id
WHERE t.token_hash = @token_hash;
