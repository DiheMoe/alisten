-- +goose Up
-- 已有的 Key 都给进过房间的成员看过，一起作废；bot 成员留给重新生成的 Key。
DROP TABLE room_api_keys;
DELETE FROM room_tickets t USING rooms r WHERE t.room_id = r.id AND t.member_id = r.bot_member_id;
