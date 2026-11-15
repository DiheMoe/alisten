package porttest

import "testing"

func TestStoreContract(t *testing.T) {
	RunStoreContract(t, func(*testing.T) ContractStore { return New() })
}

func TestClusterContract(t *testing.T) {
	RunClusterContract(t, func(*testing.T) func(string) ClusterNode {
		redis := NewRedis()
		return func(instanceID string) ClusterNode { return redis.Node(instanceID) }
	})
}
