package main

import (
	"flag"
	"os"

	"github.com/planx-lab/planx-common/logger"
	"github.com/planx-lab/planx-plugin-mysql/internal/plugin"
	planxv1 "github.com/planx-lab/planx-proto/gen/go/planx/v1"
	"github.com/planx-lab/planx-sdk-go/server"
	"github.com/planx-lab/planx-sdk-go/source"
)

func main() {
	address := flag.String("address", ":50053", "gRPC server address")
	debug := flag.Bool("debug", false, "Enable debug logging")
	flag.Parse()

	// Initialize logger
	logLevel := "info"
	if *debug {
		logLevel = "debug"
	}
	logger.Init(logger.Config{
		Level:       logLevel,
		Pretty:      true,
		Output:      os.Stdout,
		ServiceName: "planx-plugin-mysql",
	})

	// Create server
	srv := server.New(server.Config{
		Address:          *address,
		PluginName:       "mysql",
		PluginType:       server.PluginTypeSource,
		EnableReflection: true,
	})

	// Register source plugin using SDK SPI pattern
	// SDK handles: gRPC, session management, flow control
	// Plugin only provides: Init, ReadBatch, Close
	sourceServer := source.NewServer(plugin.NewMySQLSourceFactory())
	planxv1.RegisterSourcePluginServer(srv.GRPCServer(), sourceServer)

	logger.Info().Str("address", *address).Msg("Starting MySQL source plugin")

	// Run server
	if err := srv.RunWithSignals(); err != nil {
		logger.Fatal().Err(err).Msg("Server error")
	}
}
