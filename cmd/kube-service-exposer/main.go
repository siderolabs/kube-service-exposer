// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at http://mozilla.org/MPL/2.0/.

// Package main is the entry point for kube-service-exposer.
package main

import (
	"fmt"
	"log"

	"github.com/go-logr/zapr"
	"github.com/spf13/cobra"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	controllerruntimelog "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/manager/signals"

	"github.com/siderolabs/kube-service-exposer/internal/debug"
	"github.com/siderolabs/kube-service-exposer/internal/exposer"
	"github.com/siderolabs/kube-service-exposer/internal/version"
)

var rootCmdArgs struct {
	annotationKey string
	bindCIDRs     []string

	debug bool
}

// rootCmd represents the base command when called without any subcommands.
var rootCmd = &cobra.Command{
	Use:     version.Name,
	Short:   "Expose kubernetes services on specific interfaces from the configured port",
	Version: version.Tag,
	Args:    cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
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

		exposer, err := exposer.New(rootCmdArgs.annotationKey, rootCmdArgs.bindCIDRs, logger.With(zap.String("component", "exposer")))
		if err != nil {
			return err
		}

		return exposer.Run(cmd.Context())
	},
}

func main() {
	ctx := signals.SetupSignalHandler()

	if err := rootCmd.ExecuteContext(ctx); err != nil {
		log.Fatalf("failed to run: %v", err)
	}
}

func init() {
	rootCmd.Flags().StringVarP(&rootCmdArgs.annotationKey, "annotation-key", "a", version.Name+".sidero.dev/port",
		"the annotation key to be looked for on the services to determine which port to expose ot from.")
	rootCmd.Flags().StringSliceVarP(&rootCmdArgs.bindCIDRs, "bind-cidrs", "b", []string{},
		"the CIDRs to match the host IPs with. Only the ports on the IPs that match these CIDRs will be listened. When empty, all IPs will be listened.")
	rootCmd.Flags().BoolVar(&rootCmdArgs.debug, "debug", false, "enable debug logs.")
}
