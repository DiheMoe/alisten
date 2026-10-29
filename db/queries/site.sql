-- name: BrandName :one
SELECT brand_name FROM site;

-- name: SetBrandName :exec
INSERT INTO site (brand_name) VALUES (@brand_name)
ON CONFLICT (id) DO UPDATE SET brand_name = excluded.brand_name;
