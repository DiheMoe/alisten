-- +goose Up
-- 条目的轮次在入队时定下，轮流播放时点赞数相同的歌按它排列；已有的条目都算第 0 轮。
ALTER TABLE room_entries ADD COLUMN round bigint NOT NULL DEFAULT 0;
ALTER TABLE rooms
    DROP CONSTRAINT rooms_play_mode_check,
    ADD CONSTRAINT rooms_play_mode_check CHECK (play_mode IN ('sequential', 'random', 'rotation'));
