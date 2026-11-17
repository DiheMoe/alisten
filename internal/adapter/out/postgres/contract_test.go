package postgres

import (
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/dihemoe/alisten/internal/app/port/porttest"
)

func TestStoreContract(t *testing.T) {
	porttest.RunStoreContract(t, func(t *testing.T) porttest.ContractStore {
		store, pool := newStore(t)
		return contractStore{store, pool}
	})
}

type contractStore struct {
	*Store
	pool *pgxpool.Pool
}

func (s contractStore) BackdateSession(t testing.TB, tokenHash []byte, at time.Time) {
	t.Helper()
	s.backdate(t, "UPDATE member_sessions SET last_used_at = $2 WHERE token_hash = $1", tokenHash, at)
}

func (s contractStore) BackdateMember(t testing.TB, memberID string, at time.Time) {
	t.Helper()
	s.backdate(t, "UPDATE members SET created_at = $2 WHERE id = $1", memberID, at)
}

func (s contractStore) backdate(t testing.TB, sql string, key any, at time.Time) {
	t.Helper()
	tag, err := s.pool.Exec(t.Context(), sql, key, at)
	if err != nil {
		t.Fatal(err)
	}
	if tag.RowsAffected() != 1 {
		t.Fatalf("%s 改了 %d 行，期望 1 行", sql, tag.RowsAffected())
	}
}
