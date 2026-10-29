-- name: AutoDJSeeds :many
-- 只看最近 1000 次播放：按 plays (room_id, started_at) 的索引倒序读到 1000 行就停，耗时与历史的长度无关。
SELECT track_id FROM (
    SELECT DISTINCT ON (track_id) track_id, tier, rank
    FROM (
        -- 队列中的歌按等待队列的排序规则排列：轮流播放时点赞数相同的先比轮次。
        SELECT e.track_id, 0 AS tier,
               row_number() OVER (ORDER BY CASE e.slot WHEN 'current' THEN 0 WHEN 'pending' THEN 1 ELSE 2 END, e.likes DESC,
                                           CASE WHEN r.play_mode = 'rotation' THEN e.round END, e.entry_id) AS rank
        FROM room_entries e
        JOIN rooms r ON r.id = e.room_id
        WHERE e.room_id = @room_id AND e.source = 'netease' AND NOT e.auto
          AND (e.slot = 'current' OR @include_queue::boolean)
        UNION ALL
        SELECT p.track_id, 1, row_number() OVER (ORDER BY max(p.started_at) DESC)
        FROM (
            SELECT track_id, source, auto, started_at FROM plays
            WHERE room_id = @room_id
            ORDER BY started_at DESC
            LIMIT 1000
        ) p
        WHERE p.source = 'netease' AND NOT p.auto
        GROUP BY p.track_id
    ) candidates
    ORDER BY track_id, tier, rank
) seeds
ORDER BY tier, rank
LIMIT 32;

-- name: AutoDJExclusions :many
SELECT e.source, e.track_id FROM room_entries e
WHERE e.room_id = @room_id AND e.slot IN ('pending', 'waiting')
UNION
SELECT source, track_id FROM (
    SELECT source, track_id
    FROM (
        SELECT c.source, c.track_id, c.started_at FROM room_entries c
        WHERE c.room_id = @room_id AND c.slot = 'current' AND c.auto
        UNION ALL
        SELECT p.source, p.track_id, p.started_at FROM (
            SELECT source, track_id, auto, started_at FROM plays
            WHERE room_id = @room_id
            ORDER BY started_at DESC
            LIMIT 1000
        ) p
        WHERE p.auto
    ) auto_picks
    GROUP BY source, track_id
    ORDER BY max(started_at) DESC
    LIMIT 128
) recent
ORDER BY source, track_id;

-- name: TrackInUse :one
SELECT EXISTS (SELECT 1 FROM room_entries WHERE source = @source AND track_id = @track_id);
