-- +goose Up
-- 一个会话对应一个成员 token，同一个成员可以在多台设备上各有一个会话。
CREATE TABLE member_sessions (
    token_hash bytea PRIMARY KEY,
    member_id uuid NOT NULL REFERENCES members ON DELETE CASCADE,
    created_at timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX ON member_sessions (member_id);

-- bot 成员只通过 API Key 使用 HTTP 接口，不需要会话。
INSERT INTO member_sessions (token_hash, member_id, created_at)
SELECT m.token_hash, m.id, m.created_at FROM members m
WHERE NOT EXISTS (SELECT FROM rooms r WHERE r.bot_member_id = m.id);

ALTER TABLE members DROP COLUMN token_hash;

-- 设置了用户名的成员是已注册的账号，否则是游客。用户名只能由小写字母、数字、下划线和连字符组成，所以按原文唯一即可。
-- 邮箱属于账号，在所有设备上一致；游客没有账号，不替匿名身份保存个人信息，邮箱只存在游客自己的浏览器里。
ALTER TABLE members
    ADD COLUMN username text UNIQUE,
    ADD COLUMN password_hash text,
    ADD CONSTRAINT members_password_check CHECK (password_hash IS NULL OR username IS NOT NULL),
    ADD COLUMN email text,
    ADD CONSTRAINT members_email_check CHECK (email IS NULL OR username IS NOT NULL);

-- site 表没有行或者 brand_name 为 NULL 时，品牌名是默认的 Alisten；没有行时开放注册。
ALTER TABLE site
    ALTER COLUMN brand_name DROP NOT NULL,
    ADD COLUMN registration_open boolean NOT NULL DEFAULT true;

-- 票据属于签发它的会话，会话删除时票据随之失效。bot 的 API Key 票据没有会话。
ALTER TABLE room_tickets ADD COLUMN session_token_hash bytea REFERENCES member_sessions ON DELETE CASCADE;

CREATE INDEX ON room_tickets (session_token_hash);

UPDATE room_tickets t SET session_token_hash = s.token_hash
FROM member_sessions s
WHERE s.member_id = t.member_id;
