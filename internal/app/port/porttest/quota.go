package porttest

import (
	"context"
	"time"

	"github.com/dihemoe/alisten/internal/app/port"
)

var _ port.AccountQuota = Quota{}

// 零值的额度总能取到。Exhausted 里的键取不到额度。
type Quota struct {
	Exhausted map[string]bool
}

func (q Quota) Take(_ context.Context, key string, _ int, _ time.Duration) (time.Duration, error) {
	if q.Exhausted[key] {
		return time.Minute, nil
	}
	return 0, nil
}
