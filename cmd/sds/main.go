package main

import (
	. "github.com/streamingfast/cli"
	"github.com/streamingfast/logging"
	"go.uber.org/zap"
)

var zlog, _ = logging.PackageLogger("sds", "github.com/graphprotocol/substreams-data-service/cmd/sds")
var version = "dev"

func init() {
	logging.InstantiateLoggers(logging.WithDefaultLevel(zap.ErrorLevel))
}

func main() {
	Run(
		"sds",
		"Substreams Data Service CLI",
		ConfigureVersion(version),
		OnCommandErrorLogAndExit(zlog),

		devenvCmd,

		Group(
			"provider",
			"Provider-side commands",
			providerSidecarCmd,
			providerFakeOperatorCmd,
		),

		Group(
			"consumer",
			"Consumer-side commands",
			consumerSidecarCmd,
			consumerFakeClientCmd,
		),
	)
}
