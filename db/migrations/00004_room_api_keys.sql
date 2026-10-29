-- +goose Up
-- 第一次生成 API Key 时创建，此后重置 Key、修改密码都不换。
ALTER TABLE rooms
    ADD COLUMN bot_member_id uuid REFERENCES members,
    ADD CHECK (bot_member_id IS NULL OR kind = 'persistent');

-- 凭证只存哈希的唯一例外：API Key 要能随时给进过房间的成员和管理员查看。Key 就是 bot 成员的票据，票据作废时明文随之删除。
CREATE TABLE room_api_keys (
    token_hash bytea PRIMARY KEY REFERENCES room_tickets ON DELETE CASCADE,
    key text NOT NULL
);
