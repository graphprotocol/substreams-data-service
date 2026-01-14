package integration

import (
	"os"
	"testing"
)

func TestMain(m *testing.M) {
	// Integration tests are now simplified and don't require contract artifacts
	// They test EIP-712 signing and verification without blockchain deployment
	os.Exit(m.Run())
}
