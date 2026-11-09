-- +goose Up
CREATE TABLE playlists (
    id uuid PRIMARY KEY DEFAULT uuidv7(),
    owner_id uuid NOT NULL REFERENCES members ON DELETE CASCADE,
    -- 每个账号恰好一个“我喜欢”：注册时创建，不能改名或删除。
    kind text NOT NULL CHECK (kind IN ('favorites', 'custom')),
    name text NOT NULL,
    -- 新加入的曲目取 item_seq + 1 作为排序值，排在最前。
    item_seq bigint NOT NULL DEFAULT 0,
    created_at timestamptz NOT NULL DEFAULT now(),
    -- 歌单改名或曲目增减的时间。
    updated_at timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX ON playlists (owner_id);
CREATE UNIQUE INDEX ON playlists (owner_id) WHERE kind = 'favorites';

-- 展示用的元数据存一份快照：歌单里的歌不一定准备过，tracks 表里可能没有。
CREATE TABLE playlist_items (
    playlist_id uuid NOT NULL REFERENCES playlists ON DELETE CASCADE,
    source text NOT NULL,
    track_id text NOT NULL,
    title text NOT NULL,
    artist text,
    album text,
    cover_url text,
    cover_key text,
    duration_ms bigint NOT NULL,
    page_url text NOT NULL,
    position bigint NOT NULL,
    added_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (playlist_id, source, track_id)
);

CREATE INDEX ON playlist_items (playlist_id, position DESC);
