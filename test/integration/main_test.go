package integration

import (
	"context"
	"fmt"
	"os"
	"testing"

	"github.com/graphprotocol/substreams-data-service/horizon/devenv"
	"github.com/streamingfast/logging"
	"go.uber.org/zap"
)

func init() {
	logging.InstantiateLoggers(logging.WithDefaultLevel(zap.InfoLevel))
}

func TestMain(m *testing.M) {
	ctx := context.Background()
	_, err := devenv.Start(ctx)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to start development environment: %v\n", err)
		os.Exit(1)
	}
	defer devenv.Shutdown()

	os.Exit(m.Run())
}
