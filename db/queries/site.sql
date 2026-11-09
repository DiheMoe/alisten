-- name: SiteSettings :one
SELECT brand_name, registration_open FROM site;

-- name: SetBrandName :exec
INSERT INTO site (brand_name) VALUES (@brand_name)
ON CONFLICT (id) DO UPDATE SET brand_name = excluded.brand_name;

-- name: SetRegistrationOpen :exec
INSERT INTO site (registration_open) VALUES (@registration_open)
ON CONFLICT (id) DO UPDATE SET registration_open = excluded.registration_open;
