-- +goose Up
-- bot 代点的歌记下替谁点的，是 bot 填的名字。成员自己点的歌为空。轮流点歌靠它区分 bot 替不同的人点的歌。
ALTER TABLE room_entries ADD COLUMN requester text;
