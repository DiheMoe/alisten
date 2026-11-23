-- name: Credential :one
SELECT value FROM credentials WHERE name = @name;

-- name: CredentialUpdatedAt :one
SELECT updated_at FROM credentials WHERE name = @name;

-- name: SetCredential :exec
INSERT INTO credentials (name, value) VALUES (@name, @value)
ON CONFLICT (name) DO UPDATE SET value = excluded.value, updated_at = now();

-- name: SwapCredential :execrows
UPDATE credentials
SET value = @value, updated_at = now()
WHERE name = @name AND value = @expected;

-- name: DeleteCredential :exec
DELETE FROM credentials WHERE name = @name;
