package logctx

import (
	"context"
	"log/slog"
	"slices"
)

type key struct{}

type fields struct {
	request, room, member string
	job                   int64
}

func with(ctx context.Context, set func(*fields)) context.Context {
	f, _ := ctx.Value(key{}).(fields)
	set(&f)
	return context.WithValue(ctx, key{}, f)
}

func WithRequest(ctx context.Context, id string) context.Context {
	return with(ctx, func(f *fields) { f.request = id })
}

func WithRoom(ctx context.Context, id string) context.Context {
	return with(ctx, func(f *fields) { f.room = id })
}

func WithMember(ctx context.Context, id string) context.Context {
	return with(ctx, func(f *fields) { f.member = id })
}

func WithJob(ctx context.Context, id int64) context.Context {
	return with(ctx, func(f *fields) { f.job = id })
}

// 返回的 handler 自己记下 logger 打开的 group，写日志时才把它们交给 next，使关联字段写在日志的顶层而不落进
// group 中（River 的 logger 带有 river group）。往日志中添加字段的其他 handler 要放在它的内层。
func NewHandler(next slog.Handler) slog.Handler {
	return handler{next: next}
}

type handler struct {
	next   slog.Handler
	groups []group
}

type group struct {
	name  string
	attrs []slog.Attr
}

func (h handler) Enabled(ctx context.Context, level slog.Level) bool {
	return h.next.Enabled(ctx, level)
}

func (h handler) Handle(ctx context.Context, r slog.Record) error {
	out := slog.NewRecord(r.Time, r.Level, r.Message, r.PC)
	f, _ := ctx.Value(key{}).(fields)
	if f.request != "" {
		out.AddAttrs(slog.String("request_id", f.request))
	}
	if f.room != "" {
		out.AddAttrs(slog.String("room_id", f.room))
	}
	if f.member != "" {
		out.AddAttrs(slog.String("member_id", f.member))
	}
	if f.job != 0 {
		out.AddAttrs(slog.Int64("job_id", f.job))
	}
	attrs := make([]slog.Attr, 0, r.NumAttrs())
	r.Attrs(func(a slog.Attr) bool {
		attrs = append(attrs, a)
		return true
	})
	// 同一个 handler 会被并发调用，不能写入 g.attrs 多余的容量。
	for _, g := range slices.Backward(h.groups) {
		attrs = []slog.Attr{{Key: g.name, Value: slog.GroupValue(append(slices.Clip(g.attrs), attrs...)...)}}
	}
	out.AddAttrs(attrs...)
	return h.next.Handle(ctx, out)
}

// 派生的 handler 不与原来的共用切片，因为同一个 logger 可以派生出多个 logger。
func (h handler) WithAttrs(attrs []slog.Attr) slog.Handler {
	if len(h.groups) == 0 {
		return handler{next: h.next.WithAttrs(attrs)}
	}
	groups := slices.Clone(h.groups)
	last := &groups[len(groups)-1]
	last.attrs = append(slices.Clip(last.attrs), attrs...)
	return handler{next: h.next, groups: groups}
}

func (h handler) WithGroup(name string) slog.Handler {
	if name == "" {
		return h
	}
	return handler{next: h.next, groups: append(slices.Clip(h.groups), group{name: name})}
}
