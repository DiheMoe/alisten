-- +goose Up
-- 后台按 media_key 查找和删除存储的媒体，没有索引时每次都要扫全表，批量删除 50 首就要扫 100 次。
-- 只有链接和 B 站的歌有 media_key，所以只索引不为 NULL 的行。查询只按它查找，用不上唯一性，所以不加唯一约束。
CREATE INDEX ON tracks (media_key) WHERE media_key IS NOT NULL;
