-- +goose Up
-- 至多一行；没有这一行时品牌名是默认的 Alisten。
CREATE TABLE site (
    id boolean PRIMARY KEY DEFAULT true CHECK (id),
    brand_name text NOT NULL
);
