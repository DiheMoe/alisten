package postgres

import (
	"errors"
	"regexp"

	"github.com/jackc/pgx/v5/pgconn"
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

const (
	foreignKeyViolation = "23503"
	uniqueViolation     = "23505"
)

func violates(err error, code, constraint string) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == code && pgErr.ConstraintName == constraint
}
