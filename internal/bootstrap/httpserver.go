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
		// 与请求体的上限相同：房间内的 GET 和 DELETE 请求把参数放在 URL 里。
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
