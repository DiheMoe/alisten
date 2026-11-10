-- +goose Up
-- 绑定了 GitHub 的成员也是已注册的账号，可以没有用户名和密码；账号的邮箱因此也可以属于只绑定了 GitHub 的成员。
ALTER TABLE members
    ADD COLUMN github_id bigint UNIQUE,
    DROP CONSTRAINT members_email_check,
    ADD CONSTRAINT members_email_check CHECK (email IS NULL OR username IS NOT NULL OR github_id IS NOT NULL);
