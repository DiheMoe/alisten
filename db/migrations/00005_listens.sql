-- +goose Up
-- 关闭之后不再记录新的播放历史，已有的保留。
ALTER TABLE members ADD COLUMN record_listens boolean NOT NULL DEFAULT true;

-- 成员在房间里听到过的歌，每首只留最近一次。元数据存一份快照，不关联 tracks，因为管理员删除已存储的媒体时会连带删掉 tracks 中对应的行。
CREATE TABLE listens (
    member_id uuid NOT NULL REFERENCES members ON DELETE CASCADE,
    source text NOT NULL,
    track_id text NOT NULL,
    title text NOT NULL,
    artist text,
    album text,
    cover_url text,
    cover_key text,
    duration_ms bigint NOT NULL,
    page_url text NOT NULL,
    heard_at timestamptz NOT NULL,
    PRIMARY KEY (member_id, source, track_id)
);

CREATE INDEX ON listens (member_id, heard_at DESC);
