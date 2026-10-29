package postgres

import (
	"regexp"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/dihemoe/alisten/internal/adapter/out/postgres/internal/query"
)

type Store struct {
	pool *pgxpool.Pool
	q    *query.Queries
}

func New(pool *pgxpool.Pool) *Store {
	return &Store{pool: pool, q: query.New(pool)}
}

var uuidPattern = regexp.MustCompile(`^(?i)[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)

// 房间 ID 可能来自客户端：格式不正确的 uuid 参数会让 PostgreSQL 报错，而这样的房间一定不存在。
func isUUID(s string) bool {
	return uuidPattern.MatchString(s)
}

func nullable(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

func orEmpty(p *string) string {
	if p == nil {
		return ""
	}
	return *p
}
