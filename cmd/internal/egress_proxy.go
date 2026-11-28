package cmdinternal

import (
	"context"
	"log/slog"
	"net"
	"os"
	"os/signal"
	"syscall"

	"github.com/devsy-org/devsy/pkg/pool/egress/sidecar"
	"github.com/devsy-org/devsy/pkg/telemetry"
	"github.com/spf13/cobra"
)

// NewEgressProxyCmd runs the egress proxy of one pool session. The pool starts
// it as the process of the proxy container, with the config file mounted
// read-only. It needs no devsy home or provider config, so it skips the
// shared pre-run that loads them.
func NewEgressProxyCmd() *cobra.Command {
	var configPath string
	cmd := &cobra.Command{
		Use:               "egress-proxy",
		Short:             "Run the egress proxy of a pool session",
		Hidden:            true,
		Args:              cobra.NoArgs,
		SilenceUsage:      true,
		Annotations:       telemetry.NoTelemetryAnnotation(),
		PersistentPreRunE: func(*cobra.Command, []string) error { return nil },
		RunE: func(cobraCmd *cobra.Command, _ []string) error {
			return runEgressProxy(cobraCmd.Context(), configPath)
		},
	}
	cmd.Flags().StringVar(&configPath, "config", "", "path of the egress.json file")
	_ = cmd.MarkFlagRequired("config")
	return cmd
}

func runEgressProxy(ctx context.Context, configPath string) error {
	if ctx == nil {
		ctx = context.Background()
	}
	ctx, stop := signal.NotifyContext(ctx, os.Interrupt, syscall.SIGTERM)
	defer stop()
	// A second signal during the drain must end the process, so the handler
	// is removed once the first one arrives.
	go func() {
		<-ctx.Done()
		stop()
	}()
	cfg, err := sidecar.Load(configPath)
	if err != nil {
		return err
	}
	logger := slog.New(slog.NewJSONHandler(os.Stderr, nil))
	return sidecar.Run(ctx, cfg, sidecar.Options{
		Logger: logger,
		Listening: func(addr net.Addr) {
			logger.Info("egress proxy listening", "address", addr.String())
		},
	})
}
