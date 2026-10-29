-- +goose Up
ALTER TABLE room_entries ADD COLUMN page_url text NOT NULL DEFAULT '';
ALTER TABLE room_entries ALTER COLUMN page_url DROP DEFAULT;
ALTER TABLE tracks ADD COLUMN page_url text NOT NULL DEFAULT '';
ALTER TABLE tracks ALTER COLUMN page_url DROP DEFAULT;
