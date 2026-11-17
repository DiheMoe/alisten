-- +goose Up
-- 房间最多能进多少人。保留默认值 64，因为回滚到旧版本时，旧代码新建房间不写这一列，靠默认值才不会失败。
ALTER TABLE rooms ADD COLUMN capacity integer NOT NULL DEFAULT 64 CHECK (capacity BETWEEN 2 AND 128);

UPDATE rooms SET capacity = 16 WHERE kind = 'ephemeral';

-- 新建临时房间时的人数上限。site 表没有行或者 ephemeral_room_capacity 为 NULL 时，临时房间最多 16 人。
ALTER TABLE site ADD COLUMN ephemeral_room_capacity integer CHECK (ephemeral_room_capacity BETWEEN 2 AND 128);
