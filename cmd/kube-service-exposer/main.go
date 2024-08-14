// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at http://mozilla.org/MPL/2.0/.

// Package main is the entry point for kube-service-exposer.
package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net/http"
	"net/http/pprof"
	"time"

	"github.com/go-logr/zapr"
	"github.com/spf13/cobra"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"golang.org/x/sync/errgroup"
	controllerruntimelog "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/manager/signals"

	"github.com/siderolabs/kube-service-exposer/internal/debug"
	"github.com/siderolabs/kube-service-exposer/internal/exposer"
	"github.com/siderolabs/kube-service-exposer/internal/version"
)

var rootCmdArgs struct {
	annotationKey            string
	pprofBindAddr            string
	bindCIDRs                []string
	disallowedHostPortRanges []string

	debug bool
}

// rootCmd represents the base command when called without any subcommands.
var rootCmd = &cobra.Command{
	Use:     version.Name,
	Short:   "Expose kubernetes services on specific interfaces from the configured port",
	Version: version.Tag,
	Args:    cobra.NoArgs,
	RunE: func(cmd *cobra.Command, _ []string) error {
		var loggerConfig zap.Config

		if debug.Enabled {
			loggerConfig = zap.NewDevelopmentConfig()
			loggerConfig.EncoderConfig.EncodeLevel = zapcore.CapitalColorLevelEncoder
		} else {
			loggerConfig = zap.NewProductionConfig()
		}

		if !rootCmdArgs.debug {
			loggerConfig.Level.SetLevel(zap.InfoLevel)
		} else {
			loggerConfig.Level.SetLevel(zap.DebugLevel)
		}

		logger, err := loggerConfig.Build()
		if err != nil {
			return fmt.Errorf("failed to create logger: %w", err)
		}

		controllerruntimelog.SetLogger(zapr.NewLogger(logger))

		exposer, err := exposer.New(rootCmdArgs.annotationKey, rootCmdArgs.bindCIDRs, rootCmdArgs.disallowedHostPortRanges, logger.With(zap.String("component", "exposer")))
		if err != nil {
			return err
		}

		eg, ctx := errgroup.WithContext(cmd.Context())

		eg.Go(func() error {
			return exposer.Run(ctx)
		})

		if rootCmdArgs.pprofBindAddr != "" {
			eg.Go(func() error {
				return runPprofServer(ctx, logger)
			})
		}

		return eg.Wait()
	},
}

func main() {
	ctx := signals.SetupSignalHandler()

	if err := rootCmd.ExecuteContext(ctx); err != nil {
		log.Fatalf("failed to run: %v", err)
	}
}

func runPprofServer(ctx context.Context, logger *zap.Logger) error {
	logger.Info("starting pprof server", zap.String("addr", rootCmdArgs.pprofBindAddr))

	mux := http.NewServeMux()
	mux.HandleFunc("/debug/pprof/", pprof.Index)
	mux.HandleFunc("/debug/pprof/cmdline", pprof.Cmdline)
	mux.HandleFunc("/debug/pprof/profile", pprof.Profile)
	mux.HandleFunc("/debug/pprof/symbol", pprof.Symbol)
	mux.HandleFunc("/debug/pprof/trace", pprof.Trace)

	server := &http.Server{
		Addr:    rootCmdArgs.pprofBindAddr,
		Handler: mux,
	}

	errCh := make(chan error, 1)

	go func() { errCh <- server.ListenAndServe() }()

	select {
	case err := <-errCh:
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			return fmt.Errorf("failed to serve: %w", err)
		}

		return nil
	case <-ctx.Done():
	}

	logger.Info("stopping pprof server")

	shutdownCtx, shutdownCtxCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer shutdownCtxCancel()

	//nolint:contextcheck
	if err := server.Shutdown(shutdownCtx); err != nil {
		return fmt.Errorf("failed to shutdown pprof server gracefully: %w", err)
	}

	return nil
}

func init() {
	rootCmd.Flags().StringVarP(&rootCmdArgs.annotationKey, "annotation-key", "a", version.Name+".sidero.dev/port",
		"the annotation key to be looked for on the services to determine which port to expose ot from.")
	rootCmd.Flags().StringVar(&rootCmdArgs.pprofBindAddr, "pprof-bind-addr", "",
		"the address to bind the pprof server to. Disabled when empty.")
	rootCmd.Flags().StringSliceVarP(&rootCmdArgs.bindCIDRs, "bind-cidrs", "b", nil,
		"the CIDRs to match the host IPs with. Only the ports on the IPs that match these CIDRs will be listened. When empty, all IPs will be listened.")
	rootCmd.Flags().StringSliceVar(&rootCmdArgs.disallowedHostPortRanges, "disallowed-host-port-ranges", nil,
		"the port ranges on the host that are not allowed to be used. When a disallowed host port is attempted to be exposed, it will be skipped and a warning will be logged.")
	rootCmd.Flags().BoolVar(&rootCmdArgs.debug, "debug", false, "enable debug logs.")
}
