package postgres

import (
	"testing"

	"github.com/dihemoe/alisten/internal/app/port/porttest"
)

func TestStoreContract(t *testing.T) {
	porttest.RunStoreContract(t, func(t *testing.T) porttest.ContractStore {
		store, _ := newStore(t)
		return store
	})
}
