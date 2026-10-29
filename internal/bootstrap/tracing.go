package bootstrap

import (
	"context"
	"fmt"
	"log/slog"
	"slices"
	"time"

	"github.com/go-logr/logr"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.43.0"
	"go.opentelemetry.io/otel/trace"
	"go.opentelemetry.io/otel/trace/noop"
)

// serve 和 worker 自己的退出期限加上它，仍在 Kubernetes 30 秒的退出宽限期之内。
const flushTimeout = 2 * time.Second

// export 为假时返回不记录 span 的 provider，日志中也就没有 trace_id 和 span_id。导出器按 OpenTelemetry 的规范
// 自己读取 OTEL_EXPORTER_OTLP_ENDPOINT（在它的路径后加上 /v1/traces）和其他 OTEL_EXPORTER_OTLP_* 变量。
// 返回的函数要在进程退出之前调用，导出尚未发出的 span。
func newTracerProvider(ctx context.Context, export bool, service, instance string) (trace.TracerProvider, func(context.Context) error, error) {
	if !export {
		return noop.NewTracerProvider(), func(context.Context) error { return nil }, nil
	}
	exporter, err := otlptracehttp.New(ctx)
	if err != nil {
		return nil, nil, fmt.Errorf("创建 OTLP 导出器：%w", err)
	}
	res, err := resource.Merge(resource.Default(), resource.NewSchemaless(
		semconv.ServiceName(service),
		semconv.ServiceVersion(version),
		semconv.ServiceInstanceID(instance),
	))
	if err != nil {
		return nil, nil, fmt.Errorf("组装追踪的资源属性：%w", err)
	}
	provider := sdktrace.NewTracerProvider(sdktrace.WithBatcher(exporter), sdktrace.WithResource(res))
	return provider, provider.Shutdown, nil
}

// 先导出剩下的 span，再写退出日志：导出失败的告警排在退出日志之前，退出日志才是进程真正退出之前的最后一条。
// 进程出错退出时（err 不为空）由调用方记录错误，不写退出日志。
func exitAfterFlush(err error, flush func(context.Context) error, logger *slog.Logger, exited string) {
	ctx, cancel := context.WithTimeout(context.Background(), flushTimeout)
	defer cancel()
	if flushErr := flush(ctx); flushErr != nil {
		logger.WarnContext(ctx, "导出剩下的追踪失败", "error", flushErr)
	}
	if err == nil {
		logger.Info(exited)
	}
}

// OpenTelemetry 自己的日志默认以纯文本写到 stderr，导出失败之类的错误也只交给标准库 log 包。
// 它的字段放进 otel 分组：创建 tracer 的日志带有 version 字段，不能与日志的固定字段 version 重名。
// 它还会把写错的环境变量的原值记成字段（比如 OTEL_EXPORTER_OTLP_HEADERS 中的凭证），所以只保留 err 字段的值。
func routeOpenTelemetryLogs(logger *slog.Logger) {
	otel.SetLogger(logr.FromSlogHandler(errorsOnly{logger.WithGroup("otel").Handler()}))
	otel.SetErrorHandler(otel.ErrorHandlerFunc(func(err error) {
		logger.Warn("OpenTelemetry 报告错误", "error", err)
	}))
}

type errorsOnly struct{ slog.Handler }

func (h errorsOnly) Handle(ctx context.Context, r slog.Record) error {
	out := slog.NewRecord(r.Time, r.Level, r.Message, r.PC)
	r.Attrs(func(a slog.Attr) bool {
		out.AddAttrs(redactUnlessError(a))
		return true
	})
	return h.Handler.Handle(ctx, out)
}

func (h errorsOnly) WithAttrs(attrs []slog.Attr) slog.Handler {
	attrs = slices.Clone(attrs)
	for i := range attrs {
		attrs[i] = redactUnlessError(attrs[i])
	}
	return errorsOnly{h.Handler.WithAttrs(attrs)}
}

func (h errorsOnly) WithGroup(name string) slog.Handler { return errorsOnly{h.Handler.WithGroup(name)} }

// logr 把 Error 的错误参数记在 err 字段里。
func redactUnlessError(a slog.Attr) slog.Attr {
	if a.Key == "err" {
		return a
	}
	return slog.String(a.Key, redacted)
}
