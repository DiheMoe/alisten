package bootstrap

import (
	"context"
	"io"
	"log/slog"
	"strings"

	"go.opentelemetry.io/otel/trace"

	"github.com/dihemoe/alisten/internal/lib/logctx"
)

const redacted = "[REDACTED]"

var sensitiveKeyParts = []string{"cookie", "token", "ticket", "password", "secret", "authorization"}

// 不在这里加 component：slog 的 JSON 输出不合并重复字段，component 只能由各组件派生时加一次。
func newLogger(w io.Writer, level slog.Level) *slog.Logger {
	handler := slog.NewJSONHandler(w, &slog.HandlerOptions{
		Level:       level,
		ReplaceAttr: redactSensitive,
	})
	return slog.New(logctx.NewHandler(spanFields{handler})).With("service", "alisten", "version", version)
}

// 只按字段名判断，含有凭证的结构体作为一个字段记录时不会被脱敏。
func redactSensitive(_ []string, attr slog.Attr) slog.Attr {
	key := strings.ToLower(attr.Key)
	for _, part := range sensitiveKeyParts {
		if strings.Contains(key, part) {
			return slog.String(attr.Key, redacted)
		}
	}
	return attr
}

// 只有采样的 span 会导出：没有采样时不写 trace_id 和 span_id，日志才不会引用不存在的追踪。
type spanFields struct {
	slog.Handler
}

func (h spanFields) Handle(ctx context.Context, r slog.Record) error {
	if sc := trace.SpanContextFromContext(ctx); sc.IsSampled() {
		r.AddAttrs(slog.String("trace_id", sc.TraceID().String()), slog.String("span_id", sc.SpanID().String()))
	}
	return h.Handler.Handle(ctx, r)
}

func (h spanFields) WithAttrs(attrs []slog.Attr) slog.Handler {
	return spanFields{h.Handler.WithAttrs(attrs)}
}

func (h spanFields) WithGroup(name string) slog.Handler {
	return spanFields{h.Handler.WithGroup(name)}
}
