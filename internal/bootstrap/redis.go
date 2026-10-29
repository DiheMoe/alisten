package bootstrap

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"maps"
	"net"
	"os"
	"strconv"
	"sync"
	"time"

	goredis "github.com/redis/go-redis/v9"
)

func loadRedisConfig() (*goredis.Options, error) {
	host := os.Getenv("REDIS_HOST")
	if host == "" {
		return nil, errors.New("缺少 REDIS_HOST")
	}
	port, err := envPort("REDIS_PORT", 6379)
	if err != nil {
		return nil, err
	}
	value := envOr("REDIS_DB", "0")
	db, err := strconv.Atoi(value)
	if err != nil || db < 0 {
		return nil, fmt.Errorf("REDIS_DB 的值 %q 不合法，必须是非负整数", value)
	}
	return &goredis.Options{
		Addr:     net.JoinHostPort(host, strconv.Itoa(port)),
		Password: os.Getenv("REDIS_PASSWORD"),
		DB:       db,
		// 不开启时，go-redis 只按自己的读写超时等待，不理会 ctx 的期限。
		ContextTimeoutEnabled: true,
	}, nil
}

func pingRedis(ctx context.Context, rdb *goredis.Client) error {
	if err := rdb.Ping(ctx).Err(); err != nil {
		return fmt.Errorf("连接 Redis：%w", err)
	}
	return nil
}

// Redis 不可用期间，订阅连接每秒重连一次，go-redis 每次都记一条同样的日志：同样的内容一分钟内只记一次。
const redisLogRepeat = time.Minute

// go-redis 自己的日志默认以纯文本写到 stderr，接入 slog 之后进程的全部输出才都是 JSON。
type redisLogger struct {
	logger *slog.Logger

	mu     sync.Mutex
	logged map[string]time.Time
}

func newRedisLogger(logger *slog.Logger) *redisLogger {
	return &redisLogger{logger: logger, logged: map[string]time.Time{}}
}

func (l *redisLogger) Printf(ctx context.Context, format string, v ...any) {
	msg := fmt.Sprintf(format, v...)
	now := time.Now()
	l.mu.Lock()
	maps.DeleteFunc(l.logged, func(_ string, at time.Time) bool { return now.Sub(at) >= redisLogRepeat })
	_, repeated := l.logged[msg]
	if !repeated {
		l.logged[msg] = now
	}
	l.mu.Unlock()
	if !repeated {
		l.logger.WarnContext(ctx, msg)
	}
}
