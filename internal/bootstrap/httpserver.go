package bootstrap

import (
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"runtime/debug"
	"time"
)

// handler 要自己负责恢复 panic，见 recoverPanics。
func newHTTPServer(handler http.Handler, logger *slog.Logger) *http.Server {
	return &http.Server{
		Handler:           handler,
		ReadHeaderTimeout: 10 * time.Second,
		// 空闲的 keep-alive 连接两分钟后关闭，否则这些连接会一直占着 goroutine 和文件描述符，只能靠前面的 Traefik 或 nginx 断开。
		// 两分钟长于 Traefik 回收空闲上游连接的默认时长 90 秒，所以 Traefik 会先关，不会把请求发到 serve 正在关闭的连接上。
		// IdleTimeout 只作用于两次请求之间的空闲连接，被接管的 WebSocket 和正在响应的事件流不受影响。
		IdleTimeout: 2 * time.Minute,
		// 与请求体的上限相同，因为房间内的 GET 和 DELETE 请求把参数放在 URL 里。
		MaxHeaderBytes: 64 << 10,
		ErrorLog:       slog.NewLogLogger(logger.Handler(), slog.LevelError),
	}
}

// net/http 恢复 panic 时会把堆栈拼进日志消息，而堆栈要写在单独的 stack 字段里。
func recoverPanics(next http.Handler, logger *slog.Logger) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			value := recover()
			if value == nil {
				return
			}
			// ErrAbortHandler 表示有意中止响应，net/http 处理它时不记录日志。
			if err, ok := value.(error); ok && errors.Is(err, http.ErrAbortHandler) {
				panic(value)
			}
			logger.ErrorContext(r.Context(), "处理 HTTP 请求时发生 panic",
				"panic", fmt.Sprint(value),
				"method", r.Method,
				"path", r.URL.Path,
				"stack", string(debug.Stack()),
			)
			http.Error(w, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
		}()
		next.ServeHTTP(w, r)
	})
}
