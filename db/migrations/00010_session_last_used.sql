-- +goose Up
-- 会话最近一次被用到的时间，清理长期没人用的游客会话时按它筛选。为了不在每个请求上都写库，上次记录不到 24 小时就不再更新，所以精度是一天。
-- 已有的会话从迁移时刻开始算，所以上线之后最早过 7 天才会删除第一批闲置的游客会话。
ALTER TABLE member_sessions ADD COLUMN last_used_at timestamptz NOT NULL DEFAULT now();

-- 每天的清理按 last_used_at 找闲置的会话，有了索引就不用每天扫一遍全部会话。
CREATE INDEX ON member_sessions (last_used_at);

-- room_tickets.member_id 引用 members 但不级联。每删除一个成员，PostgreSQL 都要查一遍还有没有票据引用它。
-- 没有这个索引，清理时每删一个游客成员就要扫一遍 room_tickets。
CREATE INDEX ON room_tickets (member_id);
