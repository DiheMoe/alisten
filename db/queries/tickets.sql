-- name: LockRoomPassword :one
-- FOR SHARE 让修改密码和删除房间等到这次签发提交之后才进行。这样两者随后删除票据时，会把这一张也删掉。
SELECT coalesce(password_hash, '')::text AS password_hash FROM rooms WHERE id = @id FOR SHARE;

-- name: CreateTicket :exec
-- bot 的 API Key 没有会话，session_token_hash 为 NULL。
INSERT INTO room_tickets (token_hash, room_id, member_id, session_token_hash)
VALUES (@token_hash, @room_id, @member_id, sqlc.narg(session_token_hash));

-- name: TicketHolder :one
-- 顺带记录票据所属会话的使用时间，规则和 MemberByToken 相同。API Key 没有会话，不记录。
WITH used AS (
    UPDATE member_sessions s SET last_used_at = now()
    FROM room_tickets t
    WHERE t.token_hash = @token_hash AND s.token_hash = t.session_token_hash AND s.last_used_at < now() - interval '24 hours'
)
SELECT t.room_id, m.id, m.name, m.avatar_hash, r.bot_member_id IS NOT DISTINCT FROM m.id AS bot, t.session_token_hash
FROM room_tickets t
JOIN members m ON m.id = t.member_id
JOIN rooms r ON r.id = t.room_id
WHERE t.token_hash = @token_hash;

-- name: ExistingTickets :many
-- 顺带记录这些票据所属会话的使用时间，规则和 MemberByToken 相同。
WITH used AS (
    UPDATE member_sessions s SET last_used_at = now()
    FROM room_tickets t
    WHERE t.token_hash = ANY(@token_hashes::bytea[]) AND s.token_hash = t.session_token_hash AND s.last_used_at < now() - interval '24 hours'
)
SELECT token_hash FROM room_tickets WHERE token_hash = ANY(@token_hashes::bytea[]);
