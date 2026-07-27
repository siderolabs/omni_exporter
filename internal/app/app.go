// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at http://mozilla.org/MPL/2.0/.

// Package app wires up and runs the Omni exporter.
package app

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/pprof"
	"os"
	"strings"
	"time"

	"github.com/alecthomas/kingpin/v2"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/collectors"
	versioncollector "github.com/prometheus/client_golang/prometheus/collectors/version"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	promversion "github.com/prometheus/common/version"
	"github.com/prometheus/exporter-toolkit/web"
	webflag "github.com/prometheus/exporter-toolkit/web/kingpinflag"
	"github.com/siderolabs/go-api-signature/pkg/serviceaccount"
	"github.com/siderolabs/omni/client/pkg/client"
	omniclient "github.com/siderolabs/omni/client/pkg/client/omni"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"golang.org/x/sync/errgroup"

	"github.com/siderolabs/omni_exporter/internal/version"
	"github.com/siderolabs/omni_exporter/pkg/collector"
)

const (
	appName = "omni_exporter"

	envEndpoint = "OMNI_ENDPOINT"

	// maxConcurrentScrapes bounds the CPU and allocations of concurrent scrapes.
	// Scrapes render from memory plus one shared reachability probe, so a small fixed limit is enough.
	maxConcurrentScrapes = 3
)

// Run wires up the exporter from the given command line arguments and serves metrics until ctx is canceled.
func Run(ctx context.Context, args []string) error {
	app := kingpin.New(appName, "Prometheus exporter for Sidero Omni.")

	var (
		webConfig = webflag.AddFlags(app, ":10048")
		endpoint  = app.Flag("omni.endpoint",
			"Omni API endpoint, e.g. https://<account>.omni.siderolabs.io. Defaults to the "+envEndpoint+" environment variable.").
			Envar(envEndpoint).String()
		serviceAccountKeyFile = app.Flag("omni.service-account-key-file",
			"File containing the base64-encoded Omni service account key. When not set, the key is read from the "+
				serviceaccount.OmniServiceAccountKeyEnvVar+" environment variable.").String()
		insecureSkipTLSVerify = app.Flag("omni.insecure-skip-tls-verify",
			"Skip TLS verification when connecting to the Omni API.").Bool()
		enablePprof = app.Flag("web.enable-pprof",
			"Enable pprof endpoints for profiling under /debug/pprof/. "+
				"Only enable this on a trusted network, as it exposes runtime internals.").Bool()
		logLevel  = app.Flag("log.level", "Log level.").Default("info").String()
		logFormat = app.Flag("log.format", "Log format.").Default("json").Enum("json", "text")
	)

	// the version collector reads these package globals, so they are set before it is built
	promversion.Version = version.Tag
	promversion.Revision = version.SHA

	app.Version(version.Tag)
	app.HelpFlag.Short('h')

	if _, err := app.Parse(args); err != nil {
		return fmt.Errorf("failed to parse the command line: %w", err)
	}

	logger, err := buildLogger(*logLevel, *logFormat)
	if err != nil {
		return err
	}

	defer logger.Sync() //nolint:errcheck

	if *endpoint == "" {
		return fmt.Errorf("no Omni endpoint provided: set --omni.endpoint or %s", envEndpoint)
	}

	serviceAccountKey, err := loadServiceAccountKey(*serviceAccountKeyFile)
	if err != nil {
		return err
	}

	// Validate the key material locally to fail fast on malformed input.
	//
	// This deliberately does not authenticate against Omni: the client authenticates lazily,
	// and an expired or revoked service account at runtime must keep the exporter serving
	// (reporting the failure via the up metric) rather than terminate it.
	if _, err = serviceaccount.Decode(serviceAccountKey); err != nil {
		return fmt.Errorf("invalid service account key: %w", err)
	}

	omniClient, err := client.New(
		*endpoint,
		client.WithServiceAccount(serviceAccountKey),
		client.WithInsecureSkipTLSVerify(*insecureSkipTLSVerify),
		// The client resumes a broken watch stream from its last bookmark, which is cheaper than
		// the re-bootstrap it saves, and a resume the backend refuses comes back as an ordinary
		// error that drives one anyway. Its retry budget runs from the moment the stream is
		// established, not from the moment it breaks, so long-lived streams mostly fail straight
		// through to the re-bootstrap. The logger is what makes the resumes it does manage
		// visible, since they never reach the watch attempt counter.
		client.WithOmniClientOptions(omniclient.WithRetryLogger(logger)),
	)
	if err != nil {
		return fmt.Errorf("failed to create Omni client: %w", err)
	}

	defer omniClient.Close() //nolint:errcheck

	omniCollector := collector.New(omniClient.Omni().State(), collector.Options{
		Logger: logger,
	})

	registry := prometheus.NewRegistry()
	registry.MustRegister(
		omniCollector,
		collectors.NewGoCollector(),
		collectors.NewProcessCollector(collectors.ProcessCollectorOpts{}),
		versioncollector.NewCollector(appName),
	)

	landingPage, err := web.NewLandingPage(web.LandingConfig{
		Name:        "Omni Exporter",
		Description: "Prometheus exporter for Sidero Omni",
		Version:     version.Tag,
		Links: []web.LandingLinks{
			{Address: "/metrics", Text: "Metrics"},
		},
	})
	if err != nil {
		return fmt.Errorf("failed to create landing page: %w", err)
	}

	mux := http.NewServeMux()
	mux.Handle("/", landingPage)
	mux.Handle("/metrics", promhttp.HandlerFor(registry, promhttp.HandlerOpts{
		// The collector renders from memory and never returns Omni failures to promhttp
		// (those are reported via the up metric), so a gather error here can only be an
		// internal exporter bug, and those are served as a 500.
		ErrorHandling:       promhttp.HTTPErrorOnError,
		ErrorLog:            &errorLogger{logger: logger},
		MaxRequestsInFlight: maxConcurrentScrapes,
	}))

	if *enablePprof {
		logger.Info("pprof endpoints enabled")

		registerPprof(mux)
	}

	server := &http.Server{
		Handler:           mux,
		ReadTimeout:       10 * time.Second,
		ReadHeaderTimeout: 10 * time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       60 * time.Second,
	}

	return serve(ctx, server, omniCollector, webConfig, logger)
}

func registerPprof(mux *http.ServeMux) {
	mux.HandleFunc("GET /debug/pprof/", pprof.Index)
	mux.HandleFunc("GET /debug/pprof/cmdline", pprof.Cmdline)
	mux.HandleFunc("GET /debug/pprof/profile", pprof.Profile)
	mux.HandleFunc("GET /debug/pprof/symbol", pprof.Symbol)
	mux.HandleFunc("GET /debug/pprof/trace", pprof.Trace)
}

// errorLogger adapts zap to the promhttp error logger interface.
type errorLogger struct {
	logger *zap.Logger
}

func (l *errorLogger) Println(v ...any) {
	l.logger.Error(fmt.Sprintln(v...))
}

// loadServiceAccountKey loads the service account key from the given file or from the environment.
func loadServiceAccountKey(keyFile string) (string, error) {
	if keyFile != "" {
		content, err := os.ReadFile(keyFile)
		if err != nil {
			return "", fmt.Errorf("failed to read the service account key file: %w", err)
		}

		return strings.TrimSpace(string(content)), nil
	}

	if key := os.Getenv(serviceaccount.OmniServiceAccountKeyEnvVar); key != "" {
		return key, nil
	}

	return "", fmt.Errorf("no service account key provided: set --omni.service-account-key-file or %s",
		serviceaccount.OmniServiceAccountKeyEnvVar)
}

// serve runs the watch loops and the HTTP server until ctx is canceled.
func serve(ctx context.Context, server *http.Server, omniCollector *collector.Collector, webConfig *web.FlagConfig, logger *zap.Logger) error {
	logger.Info(
		"starting the server",
		zap.String("version", version.Tag),
		zap.Strings("listen_addresses", *webConfig.WebListenAddresses),
	)

	eg, ctx := errgroup.WithContext(ctx)

	eg.Go(func() error {
		return omniCollector.Run(ctx)
	})

	eg.Go(func() error {
		<-ctx.Done()

		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()

		logger.Info("shutting down")

		return server.Shutdown(shutdownCtx) //nolint:contextcheck
	})

	eg.Go(func() error {
		if err := web.ListenAndServe(server, webConfig, slog.New(slog.DiscardHandler)); err != nil && !errors.Is(err, http.ErrServerClosed) {
			return fmt.Errorf("failed to serve: %w", err)
		}

		return nil
	})

	return eg.Wait()
}

// buildLogger initializes the logger the same way Omni does.
func buildLogger(level, format string) (*zap.Logger, error) {
	var loggerConfig zap.Config

	if format == "text" {
		loggerConfig = zap.NewDevelopmentConfig()
		loggerConfig.EncoderConfig.EncodeLevel = zapcore.CapitalColorLevelEncoder
	} else {
		loggerConfig = zap.NewProductionConfig()
	}

	var zapLevel zapcore.Level
	if err := zapLevel.UnmarshalText([]byte(level)); err != nil {
		return nil, fmt.Errorf("invalid log level %q: %w", level, err)
	}

	loggerConfig.Level.SetLevel(zapLevel)

	return loggerConfig.Build(zap.AddStacktrace(zapcore.FatalLevel)) // only print stack traces for fatal errors
}
