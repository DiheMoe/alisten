-- +goose Up
-- 分享链接中的随机串，NULL 表示没有分享。明文保存：所有者随时可以再复制一次链接。
ALTER TABLE playlists ADD COLUMN share_id text UNIQUE;
