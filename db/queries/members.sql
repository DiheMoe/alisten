-- name: CreateMember :one
INSERT INTO members (token_hash, name) VALUES (@token_hash, @name)
RETURNING *;

-- name: MemberByToken :one
SELECT * FROM members WHERE token_hash = @token_hash;

-- name: UpdateMember :one
-- 只覆盖传入的列，分别修改昵称和头像的两个并发请求因此不会互相还原；头像哈希传入空串表示清除。
UPDATE members
SET name = coalesce(sqlc.narg(name), name),
    avatar_hash = CASE WHEN sqlc.narg(avatar_hash)::text IS NULL THEN avatar_hash ELSE NULLIF(sqlc.narg(avatar_hash)::text, '') END
WHERE id = @id
RETURNING *;
