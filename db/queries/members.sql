-- name: CreateMember :one
INSERT INTO members (name) VALUES (@name)
RETURNING *;

-- name: CreateSession :exec
INSERT INTO member_sessions (token_hash, member_id) VALUES (@token_hash, @member_id);

-- name: MemberByToken :one
SELECT * FROM members WHERE id = (SELECT member_id FROM member_sessions WHERE token_hash = @token_hash);

-- name: UpdateMember :one
-- 只覆盖传入的列，分别修改昵称和头像的两个并发请求因此不会互相还原；头像哈希和邮箱传入空串表示清除。游客的邮箱总是 NULL。
UPDATE members
SET name = coalesce(sqlc.narg(name), name),
    avatar_hash = CASE WHEN sqlc.narg(avatar_hash)::text IS NULL THEN avatar_hash ELSE NULLIF(sqlc.narg(avatar_hash)::text, '') END,
    email = CASE WHEN username IS NULL THEN NULL WHEN sqlc.narg(email)::text IS NULL THEN email ELSE NULLIF(sqlc.narg(email)::text, '') END
WHERE id = @id
RETURNING *;

-- name: MemberAccountProfile :one
SELECT username, email FROM members WHERE id = @id;

-- name: RegisterMember :execrows
-- 只给游客设置：已经注册过的成员不受影响，影响的行数为 0。
UPDATE members SET username = @username, password_hash = @password_hash
WHERE id = @id AND username IS NULL;

-- name: AccountByUsername :one
SELECT * FROM members WHERE username = @username;

-- name: SetMemberPassword :execrows
UPDATE members SET password_hash = @password_hash WHERE id = @id AND username IS NOT NULL;

-- name: LockAccountPassword :one
-- 用互斥的 FOR NO KEY UPDATE，不用 FOR SHARE：同一个账号的修改和重置依次进行，后一个读到的是前一个写入的密码哈希。
SELECT coalesce(password_hash, '')::text AS password_hash FROM members WHERE id = @id AND username IS NOT NULL FOR NO KEY UPDATE;

-- name: MemberHasSession :one
SELECT EXISTS (SELECT FROM member_sessions WHERE token_hash = @token_hash AND member_id = @member_id);

-- name: LockMemberPassword :one
-- 让修改密码等到这次登录提交之后，它随后删除会话时会删掉这次登录签发的会话。
SELECT coalesce(password_hash, '')::text AS password_hash FROM members WHERE id = @id FOR NO KEY UPDATE;

-- name: DeleteSession :execrows
DELETE FROM member_sessions WHERE token_hash = @token_hash;

-- name: DeleteMemberSessions :many
-- keep 为 NULL 时删除这个成员的全部会话。
DELETE FROM member_sessions WHERE member_id = @member_id AND token_hash IS DISTINCT FROM sqlc.narg(keep)::bytea
RETURNING token_hash;
