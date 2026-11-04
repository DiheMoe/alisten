-- +goose Up
ALTER TABLE tracks
    ADD COLUMN lyric_words text,
    ADD COLUMN lyric_trans text;
