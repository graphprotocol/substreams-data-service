package integration

import (
	"sync"
	"testing"

	"github.com/streamingfast/eth-go"
)

// TestEnv holds the test environment state
// For now, this is simplified to just provide a consistent domain and addresses
// without requiring actual blockchain deployment
type TestEnv struct {
	ChainID          uint64
	CollectorAddress eth.Address
}

var (
	sharedEnv     *TestEnv
	sharedEnvOnce sync.Once
)

// SetupEnv returns a shared test environment
// This is a simplified version that doesn't require Docker or blockchain deployment
// It provides a consistent test environment with fixed chain ID and contract address
func SetupEnv(t *testing.T) *TestEnv {
	t.Helper()
	sharedEnvOnce.Do(func() {
		sharedEnv = &TestEnv{
			ChainID:          1337, // Standard test chain ID
			CollectorAddress: eth.MustNewAddress("0x1234567890123456789012345678901234567890"),
		}
	})
	return sharedEnv
}
