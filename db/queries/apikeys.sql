-- name: LockRoomBot :one
-- 用互斥的 FOR NO KEY UPDATE，不用 FOR SHARE：同一个房间的重置要依次进行，
-- 同时到达的首次生成才只会创建一个 bot 成员，最后只留下一把 Key。
SELECT bot_member_id FROM rooms WHERE id = @id FOR NO KEY UPDATE;

-- name: SetRoomBot :exec
UPDATE rooms SET bot_member_id = @bot_member_id::uuid WHERE id = @id;

-- name: BotTicketCreatedAt :one
SELECT t.created_at FROM room_tickets t JOIN rooms r ON r.id = t.room_id AND r.bot_member_id = t.member_id
WHERE t.room_id = @room_id;

-- name: DeleteMemberTickets :exec
DELETE FROM room_tickets WHERE room_id = @room_id AND member_id = @member_id;
