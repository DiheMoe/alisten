-- name: Credential :one
SELECT value FROM credentials WHERE name = @name;

-- name: CredentialWithUpdatedAt :one
SELECT value, updated_at FROM credentials WHERE name = @name;

-- name: SetCredential :exec
INSERT INTO credentials (name, value) VALUES (@name, @value)
ON CONFLICT (name) DO UPDATE SET value = excluded.value, updated_at = now();

-- name: DeleteCredential :exec
DELETE FROM credentials WHERE name = @name;
