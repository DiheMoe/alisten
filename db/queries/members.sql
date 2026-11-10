-- name: CreateMember :one
INSERT INTO members (name) VALUES (@name)
RETURNING *;

-- name: CreateSession :exec
INSERT INTO member_sessions (token_hash, member_id) VALUES (@token_hash, @member_id);

-- name: MemberByToken :one
SELECT * FROM members WHERE id = (SELECT member_id FROM member_sessions WHERE token_hash = @token_hash);

-- name: UpdateMember :one
-- 只覆盖传入的列，所以分别修改昵称和头像的两个并发请求不会互相还原。头像哈希和邮箱传入空串表示清除。游客的邮箱总是 NULL。
UPDATE members
SET name = coalesce(sqlc.narg(name), name),
    avatar_hash = CASE WHEN sqlc.narg(avatar_hash)::text IS NULL THEN avatar_hash ELSE NULLIF(sqlc.narg(avatar_hash)::text, '') END,
    email = CASE WHEN username IS NULL AND github_id IS NULL THEN NULL WHEN sqlc.narg(email)::text IS NULL THEN email ELSE NULLIF(sqlc.narg(email)::text, '') END
WHERE id = @id
RETURNING *;

-- name: MemberAccountProfile :one
SELECT username, email, github_id FROM members WHERE id = @id;

-- name: RegisterMember :execrows
-- 只给游客设置。只绑定了 GitHub 的账号补设用户名和密码时还要删除其他会话，所以用 AddMemberCredentials。
UPDATE members SET username = @username, password_hash = @password_hash
WHERE id = @id AND username IS NULL AND github_id IS NULL;

-- name: AddMemberCredentials :execrows
-- 只给只绑定了 GitHub 的账号设置。这种账号的“我喜欢”已经在绑定时创建了。
UPDATE members SET username = @username, password_hash = @password_hash
WHERE id = @id AND username IS NULL AND github_id IS NOT NULL;

-- name: AccountByUsername :one
SELECT * FROM members WHERE username = @username;

-- name: AccountByID :one
SELECT * FROM members WHERE id = @id AND (username IS NOT NULL OR github_id IS NOT NULL);

-- name: AccountByGitHub :one
SELECT * FROM members WHERE github_id = @github_id;

-- name: BindGitHub :one
-- 只给还没有绑定 GitHub 的成员绑定。已经绑定过的成员不受影响，查询没有返回的行。was_guest 表示成员原来是游客，这次绑定就是注册。
UPDATE members SET github_id = @github_id
WHERE id = @id AND github_id IS NULL
RETURNING (username IS NULL)::boolean AS was_guest;

-- name: UnbindGitHub :execrows
-- 没有密码的账号解绑之后就再也无法登录，所以只给设置了密码的账号解绑。
UPDATE members SET github_id = NULL WHERE id = @id AND password_hash IS NOT NULL;

-- name: SetMemberPassword :execrows
UPDATE members SET password_hash = @password_hash WHERE id = @id AND username IS NOT NULL;

-- name: LockAccountPassword :one
-- 用互斥的 FOR NO KEY UPDATE，不用 FOR SHARE。这样同一个账号的修改和重置依次进行，后一个读到的是前一个写入的密码哈希。
SELECT coalesce(password_hash, '')::text AS password_hash FROM members WHERE id = @id AND username IS NOT NULL FOR NO KEY UPDATE;

-- name: MemberHasSession :one
SELECT EXISTS (SELECT FROM member_sessions WHERE token_hash = @token_hash AND member_id = @member_id);

-- name: LockMemberPassword :one
-- 这把锁让修改密码等到这次登录提交之后才进行，所以修改密码随后删除会话时，会删掉这次登录签发的会话。
-- 用互斥的 FOR NO KEY UPDATE，不用 FOR SHARE，这样同一个账号的几次登录会依次并入游客的历史。否则两次登录会按相反的顺序抢同一批历史行而死锁。
SELECT coalesce(password_hash, '')::text AS password_hash FROM members WHERE id = @id FOR NO KEY UPDATE;

-- name: DeleteSession :execrows
DELETE FROM member_sessions WHERE token_hash = @token_hash;

-- name: TakeSession :one
DELETE FROM member_sessions WHERE token_hash = @token_hash RETURNING member_id;

-- name: DeleteMemberSessions :many
-- keep 为 NULL 时删除这个成员的全部会话。
DELETE FROM member_sessions WHERE member_id = @member_id AND token_hash IS DISTINCT FROM sqlc.narg(keep)::bytea
RETURNING token_hash;
