-- +goose Up
-- 可选的文本值在没有或不适用时为 NULL；空串只表示值本身为空，例如被清空的房间描述。
CREATE TABLE members (
    id uuid PRIMARY KEY DEFAULT uuidv7(),
    token_hash bytea NOT NULL UNIQUE,
    name text NOT NULL,
    avatar_hash text,
    created_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE rooms (
    id uuid PRIMARY KEY DEFAULT uuidv7(),
    kind text NOT NULL CHECK (kind IN ('persistent', 'ephemeral')),
    slug text UNIQUE CHECK (slug IS NULL OR kind = 'persistent'),
    name text NOT NULL,
    description text NOT NULL,
    password_hash text,
    -- 接管房间时加 1；落盘以它为条件，因此过期的 owner 无法写入。
    term bigint NOT NULL DEFAULT 0,
    play_mode text NOT NULL DEFAULT 'sequential' CHECK (play_mode IN ('sequential', 'random', 'rotation')),
    entry_seq bigint NOT NULL DEFAULT 0,
    created_at timestamptz NOT NULL DEFAULT now(),
    -- 第一次生成 API Key 时创建，此后重置 Key、修改密码都不换。
    bot_member_id uuid REFERENCES members,
    CHECK (bot_member_id IS NULL OR kind = 'persistent')
);

CREATE TABLE room_entries (
    room_id uuid NOT NULL REFERENCES rooms ON DELETE CASCADE,
    entry_id bigint NOT NULL,
    slot text NOT NULL CHECK (slot IN ('waiting', 'pending', 'current')),
    source text NOT NULL,
    track_id text NOT NULL,
    title text NOT NULL,
    artist text,
    album text,
    cover_url text,
    cover_key text,
    meta_duration_ms bigint NOT NULL,
    -- AutoDJ 加入的条目没有点歌的成员，by_* 都为 NULL。
    by_member_id uuid,
    by_name text,
    by_avatar_hash text,
    auto boolean NOT NULL,
    likes integer NOT NULL,
    added_at timestamptz NOT NULL,
    started_at timestamptz CHECK ((slot = 'current') = (started_at IS NOT NULL)),
    page_url text NOT NULL,
    -- 条目的轮次在入队时定下，轮流播放时点赞数相同的歌按它排列。
    round bigint NOT NULL DEFAULT 0,
    PRIMARY KEY (room_id, entry_id)
);

CREATE INDEX ON room_entries (source, track_id);

CREATE TABLE room_tickets (
    token_hash bytea PRIMARY KEY,
    room_id uuid NOT NULL REFERENCES rooms ON DELETE CASCADE,
    member_id uuid NOT NULL REFERENCES members,
    created_at timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX ON room_tickets (room_id);

-- 只保存准备成功的结果。
CREATE TABLE tracks (
    source text NOT NULL,
    track_id text NOT NULL,
    title text NOT NULL,
    artist text,
    album text,
    cover_url text,
    cover_key text,
    duration_ms bigint NOT NULL,
    gain double precision NOT NULL,
    lyric text,
    media_key text,
    media_size bigint,
    prepared_at timestamptz NOT NULL DEFAULT now(),
    -- 临时房间的播放记录随房间删除，所以不从 plays 计算。
    last_played_at timestamptz,
    page_url text NOT NULL,
    PRIMARY KEY (source, track_id)
);

CREATE TABLE plays (
    id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    room_id uuid NOT NULL REFERENCES rooms ON DELETE CASCADE,
    source text NOT NULL,
    track_id text NOT NULL,
    by_member_id uuid,
    by_name text,
    by_avatar_hash text,
    auto boolean NOT NULL,
    started_at timestamptz NOT NULL,
    ended_at timestamptz NOT NULL
);

CREATE INDEX ON plays (room_id, started_at);

-- 只有已设置的凭据才有对应的行。
CREATE TABLE credentials (
    name text PRIMARY KEY,
    value text NOT NULL,
    updated_at timestamptz NOT NULL DEFAULT now()
);

-- 至多一行；没有这一行时品牌名是默认的 Alisten。
CREATE TABLE site (
    id boolean PRIMARY KEY DEFAULT true CHECK (id),
    brand_name text NOT NULL
);
