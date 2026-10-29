package bootstrap

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"sync"
	"unicode"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	goredis "github.com/redis/go-redis/v9"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	semconv "go.opentelemetry.io/otel/semconv/v1.43.0"
	"go.opentelemetry.io/otel/trace"
	"go.opentelemetry.io/otel/trace/noop"

	"github.com/dihemoe/alisten/internal/app/port"
)

const scope = "github.com/dihemoe/alisten/internal/bootstrap"

// 外部调用只在已采样的 trace 中记录子 span：续约、心跳、对账和任务轮询等后台调用每隔几秒就有一次，不能各自成为一条 trace。
// 父 span 已经结束时也照样记录：owner 处理收到的输入、提交准备任务都发生在发起它们的 span 结束之后。
// 需要计算名字和属性的调用先用它判断，没有采样的后台调用就不必付出这份开销。
func inSampledTrace(ctx context.Context) bool {
	return trace.SpanContextFromContext(ctx).IsSampled()
}

func startChild(ctx context.Context, tracer trace.Tracer, name string, opts ...trace.SpanStartOption) (context.Context, trace.Span) {
	if !inSampledTrace(ctx) {
		return ctx, noop.Span{}
	}
	return tracer.Start(ctx, name, append(opts, trace.WithSpanKind(trace.SpanKindClient))...)
}

// otelriver 提交任务时总是开始 producer span：没有发起它的请求时（接管之后重新准备、AutoDJ），它会自成一条 trace。
// 这里只在已采样的 trace 中开始它；没有 span 时，任务的元数据中也没有追踪上下文。执行任务的 consumer span 不受影响，照常另开 trace。
type insertsInTraces struct{ trace.TracerProvider }

func (p insertsInTraces) Tracer(name string, opts ...trace.TracerOption) trace.Tracer {
	return insertTracer{p.TracerProvider.Tracer(name, opts...)}
}

type insertTracer struct{ trace.Tracer }

func (t insertTracer) Start(ctx context.Context, name string, opts ...trace.SpanStartOption) (context.Context, trace.Span) {
	cfg := trace.NewSpanStartConfig(opts...)
	if cfg.SpanKind() == trace.SpanKindProducer && !inSampledTrace(ctx) {
		return ctx, noop.Span{}
	}
	return t.Tracer.Start(ctx, name, opts...)
}

// span 的状态只记出错，不记错误的文本：上游的错误中可能有带签名的地址，它们只写进日志。
func recordError(span trace.Span, err error) error {
	if err != nil {
		span.SetStatus(codes.Error, "")
	}
	return err
}

// 只记 SQL 语句，不记参数。
func traceDatabase(cfg *pgxpool.Config, tracer trace.TracerProvider) {
	cfg.ConnConfig.Tracer = databaseSpans{tracer: tracer.Tracer(scope)}
}

type databaseSpans struct {
	tracer trace.Tracer
}

// 在 ctx 中记下 TraceQueryStart 开始的 span：TraceQueryEnd 只结束这个 span，不能结束 ctx 中原有的 span。
type querySpan struct{}

func (d databaseSpans) TraceQueryStart(ctx context.Context, _ *pgx.Conn, data pgx.TraceQueryStartData) context.Context {
	if !inSampledTrace(ctx) {
		return ctx
	}
	return d.start(ctx, queryName(data.SQL), semconv.DBQueryText(data.SQL))
}

func (d databaseSpans) TraceQueryEnd(ctx context.Context, _ *pgx.Conn, data pgx.TraceQueryEndData) {
	endQuery(ctx, data.Err)
}

func (d databaseSpans) TraceCopyFromStart(ctx context.Context, _ *pgx.Conn, data pgx.TraceCopyFromStartData) context.Context {
	if !inSampledTrace(ctx) {
		return ctx
	}
	return d.start(ctx, "COPY "+data.TableName.Sanitize(), semconv.DBCollectionName(data.TableName.Sanitize()))
}

func (d databaseSpans) TraceCopyFromEnd(ctx context.Context, _ *pgx.Conn, data pgx.TraceCopyFromEndData) {
	endQuery(ctx, data.Err)
}

func (d databaseSpans) start(ctx context.Context, name string, attr attribute.KeyValue) context.Context {
	ctx, span := startChild(ctx, d.tracer, name, trace.WithAttributes(semconv.DBSystemNamePostgreSQL, attr))
	if !span.IsRecording() {
		return ctx
	}
	return context.WithValue(ctx, querySpan{}, span)
}

func endQuery(ctx context.Context, err error) {
	if span, ok := ctx.Value(querySpan{}).(trace.Span); ok {
		_ = recordError(span, err)
		span.End()
	}
}

// sqlc 和 River 的查询都以"-- name: 查询名 :类型"开头，span 以查询名命名；其他语句取第一个词，比如 BEGIN、COMMIT。
// 只看第一个词：River 每秒轮询的查询有 1.5 KB，不必扫描整条语句。
func queryName(sql string) string {
	rest, named := strings.CutPrefix(sql, "-- name: ")
	word := strings.TrimLeftFunc(rest, unicode.IsSpace)
	if end := strings.IndexFunc(word, unicode.IsSpace); end >= 0 {
		word = word[:end]
	}
	switch {
	case word == "":
		return "QUERY"
	case named:
		return word
	}
	return strings.ToUpper(word)
}

func traceRedis(rdb *goredis.Client, tracer trace.TracerProvider) {
	rdb.AddHook(redisSpans{tracer: tracer.Tracer(scope)})
}

// 只记命令名，不记参数：实例间消息中有成员资料和聊天内容。
type redisSpans struct {
	tracer trace.Tracer
}

func (redisSpans) DialHook(next goredis.DialHook) goredis.DialHook {
	return next
}

func (h redisSpans) ProcessHook(next goredis.ProcessHook) goredis.ProcessHook {
	return func(ctx context.Context, cmd goredis.Cmder) error {
		if !inSampledTrace(ctx) {
			return next(ctx, cmd)
		}
		ctx, span := startChild(ctx, h.tracer, cmd.FullName(), trace.WithAttributes(semconv.DBSystemNameRedis))
		defer span.End()
		return redisError(span, next(ctx, cmd))
	}
}

func (h redisSpans) ProcessPipelineHook(next goredis.ProcessPipelineHook) goredis.ProcessPipelineHook {
	return func(ctx context.Context, cmds []goredis.Cmder) error {
		if !inSampledTrace(ctx) {
			return next(ctx, cmds)
		}
		ctx, span := startChild(ctx, h.tracer, "pipeline", trace.WithAttributes(semconv.DBSystemNameRedis))
		defer span.End()
		return redisError(span, next(ctx, cmds))
	}
}

// goredis.Nil 表示键不存在，不是出错。NOSCRIPT 是 Script.Run 的正常回退：脚本不在 Redis 的缓存中时（Redis 重启或清空缓存之后
// 每个脚本的第一次调用），EVALSHA 先收到它，随后改用 EVAL。
func redisError(span trace.Span, err error) error {
	if errors.Is(err, goredis.Nil) || goredis.HasErrorPrefix(err, "NOSCRIPT") {
		return err
	}
	return recordError(span, err)
}

// 只记方法、主机名和状态码：音源的地址中有签名、vkey 和用户的 ID。也不向音源传递追踪上下文。
// span 在响应体读完或关闭时结束，下载音频的时间也算在其中。
func traceHTTP(next http.RoundTripper, tracer trace.TracerProvider) http.RoundTripper {
	return clientSpans{next: next, tracer: tracer.Tracer(scope)}
}

type clientSpans struct {
	next   http.RoundTripper
	tracer trace.Tracer
}

func (t clientSpans) RoundTrip(r *http.Request) (*http.Response, error) {
	if !inSampledTrace(r.Context()) {
		return t.next.RoundTrip(r)
	}
	ctx, span := startChild(r.Context(), t.tracer, "HTTP "+r.Method, trace.WithAttributes(
		semconv.HTTPRequestMethodKey.String(r.Method),
		semconv.ServerAddress(r.URL.Hostname()),
	))
	if !span.IsRecording() {
		return t.next.RoundTrip(r)
	}
	resp, err := t.next.RoundTrip(r.WithContext(ctx))
	if err != nil {
		_ = recordError(span, err)
		span.End()
		return nil, err
	}
	span.SetAttributes(semconv.HTTPResponseStatusCode(resp.StatusCode))
	if resp.StatusCode >= http.StatusBadRequest {
		span.SetStatus(codes.Error, http.StatusText(resp.StatusCode))
	}
	resp.Body = &spanBody{ReadCloser: resp.Body, span: span}
	return resp, nil
}

type spanBody struct {
	io.ReadCloser
	span trace.Span
	once sync.Once
}

func (b *spanBody) Read(p []byte) (int, error) {
	n, err := b.ReadCloser.Read(p)
	if err != nil {
		b.end()
	}
	return n, err
}

func (b *spanBody) Close() error {
	b.end()
	return b.ReadCloser.Close()
}

func (b *spanBody) end() {
	b.once.Do(func() { b.span.End() })
}

func traceDownloads(download port.LinkDownloader, tracer trace.TracerProvider) port.LinkDownloader {
	return tracedDownloads{LinkDownloader: download, tracer: tracer.Tracer(scope)}
}

type tracedDownloads struct {
	port.LinkDownloader
	tracer trace.Tracer
}

func (d tracedDownloads) Download(ctx context.Context, link string) (port.LinkDownload, func(), error) {
	ctx, span := startChild(ctx, d.tracer, "yt-dlp")
	defer span.End()
	result, release, err := d.LinkDownloader.Download(ctx, link)
	return result, release, recordError(span, err)
}

func traceObjects(store port.MediaStore, tracer trace.TracerProvider) port.MediaStore {
	return tracedObjects{MediaStore: store, tracer: tracer.Tracer(scope)}
}

type tracedObjects struct {
	port.MediaStore
	tracer trace.Tracer
}

func (s tracedObjects) Put(ctx context.Context, key string, body io.ReadSeeker, contentType string) error {
	ctx, span := startChild(ctx, s.tracer, "S3 PutObject")
	defer span.End()
	return recordError(span, s.MediaStore.Put(ctx, key, body, contentType))
}

func (s tracedObjects) Exists(ctx context.Context, key string) (bool, error) {
	ctx, span := startChild(ctx, s.tracer, "S3 HeadObject")
	defer span.End()
	ok, err := s.MediaStore.Exists(ctx, key)
	return ok, recordError(span, err)
}

func (s tracedObjects) Delete(ctx context.Context, key string) error {
	ctx, span := startChild(ctx, s.tracer, "S3 DeleteObject")
	defer span.End()
	return recordError(span, s.MediaStore.Delete(ctx, key))
}
