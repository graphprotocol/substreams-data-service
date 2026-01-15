package integration

import (
	"github.com/streamingfast/logging"
)

var zlog, tracer = logging.PackageLogger("integration_tests", "github.com/streamingfast/horizon-go/test/integration")

func init() {
	logging.InstantiateLoggers()
}
