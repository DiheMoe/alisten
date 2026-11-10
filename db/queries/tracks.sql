-- name: PreparedTrack :one
SELECT title, artist, album, cover_url, cover_key, page_url, duration_ms, gain, lyric, lyric_words, lyric_trans, media_key, media_size FROM tracks
WHERE source = @source AND track_id = @track_id;

-- name: SavePreparedTrack :exec
-- 准备任务只对未完成的任务去重，所以同一首歌可能先后准备两次，以后一次的结果为准。
INSERT INTO tracks (source, track_id, title, artist, album, cover_url, cover_key, page_url, duration_ms, gain, lyric, lyric_words, lyric_trans, media_key, media_size)
VALUES (@source, @track_id, @title, @artist, @album, @cover_url, @cover_key, @page_url, @duration_ms, @gain, @lyric, @lyric_words, @lyric_trans, @media_key, @media_size)
ON CONFLICT (source, track_id) DO UPDATE SET
    title = excluded.title, artist = excluded.artist, album = excluded.album, cover_url = excluded.cover_url,
    cover_key = excluded.cover_key, page_url = excluded.page_url, duration_ms = excluded.duration_ms, gain = excluded.gain,
    lyric = excluded.lyric, lyric_words = excluded.lyric_words, lyric_trans = excluded.lyric_trans,
    media_key = excluded.media_key, media_size = excluded.media_size,
    prepared_at = now();

-- name: TrackMetas :many
SELECT t.source, t.track_id, t.title, t.artist, t.album, t.cover_url, t.cover_key, t.page_url, t.duration_ms
FROM unnest(@sources::text[]) WITH ORDINALITY AS s(source, ord)
JOIN unnest(@track_ids::text[]) WITH ORDINALITY AS k(track_id, ord) USING (ord)
JOIN tracks t ON t.source = s.source AND t.track_id = k.track_id;
