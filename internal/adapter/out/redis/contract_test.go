package redis

import (
	"log/slog"
	"testing"

	"github.com/prometheus/client_golang/prometheus"
	"go.opentelemetry.io/otel/trace/noop"

	"github.com/dihemoe/alisten/internal/app/port/porttest"
)

type clusterNode struct {
	*Leases
	*Liveness
	*Bus
}

func TestClusterContract(t *testing.T) {
	porttest.RunClusterContract(t, func(t *testing.T) func(string) porttest.ClusterNode {
		client := newClient(t)
		return func(instanceID string) porttest.ClusterNode {
			return clusterNode{
				Leases:   NewLeases(client, instanceID),
				Liveness: NewLiveness(client, instanceID),
				Bus:      NewBus(client, instanceID, "v1", prometheus.NewRegistry(), noop.NewTracerProvider(), slog.New(slog.DiscardHandler)),
			}
		}
	})
}
