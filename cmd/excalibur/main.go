// nolint: mnd // ...
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"excalibur/internal/config"
	"excalibur/internal/datasource"
	"excalibur/internal/logging"
	"excalibur/internal/report"
)

func main() {
	// Initial, minimal flag parsing just to set the log level early.
	// The main flag parsing happens within run().
	verbose := flag.Bool("verbose", false, "Enable verbose (debug) logging.")
	_ = flag.CommandLine.Parse(os.Args[1:])

	logger := logging.NewLogger(os.Stdout, *verbose)

	if err := run(os.Args[1:], os.Getenv, logger); err != nil {
		if !errors.Is(err, flag.ErrHelp) {
			logger.Error("Application failed", slog.String("error", err.Error()))
			os.Exit(1)
		}
		os.Exit(0)
	}

	logger.Debug("Application finished successfully.")
}

func run(args []string, getenv func(string) string, logger *slog.Logger) error {
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	logger.Info("Starting Excalibur")

	// --- Configuration ---
	logger.Debug("Loading configuration...")
	cfgRaw, err := config.Load(args, getenv, logger)
	if err != nil {
		if !errors.Is(err, flag.ErrHelp) {
			logger.Error("Failed to load configuration", slog.String("error", err.Error()))
		}
		return err
	}

	logger.Debug("Validating configuration...")
	if err = config.Validate(ctx, cfgRaw, logger); err != nil {
		logger.Error("Configuration validation failed", slog.String("error", err.Error()))
		return fmt.Errorf("validate config: %w", err)
	}

	logger.Debug("Normalizing configuration...")
	cfg, err := config.Normalize(cfgRaw, logger)
	if err != nil {
		logger.Error("Configuration normalization failed", slog.String("error", err.Error()))
		return fmt.Errorf("normalize config: %w", err)
	}

	logger.Debug("Using normalized configuration",
		slog.Group("report",
			slog.String("template_path", cfg.Report.TemplatePath),
			slog.String("output_path", cfg.Report.OutputPath),
			slog.String("queries_dir", cfg.Report.QueriesDir),
			slog.String("ref_column", cfg.Report.DataSourceRefColumn),
			slog.Duration("timeout", cfg.Report.Timeout),
		),
		slog.Group("datasource",
			slog.String("dsn_provided", maskDSNPassword(cfg.DataSource.DSN)),
		),
	)
	logger.Debug("Full DSN", slog.String("dsn", cfg.DataSource.DSN)) // Only in verbose mode

	// --- Datasource Setup ---
	logger.Info("Initializing data source...")
	postgresSource, err := datasource.NewPostgresDataSource(ctx, cfg.DataSource, logger)
	if err != nil {
		logger.Error("Failed to initialize data source", slog.String("error", err.Error()))
		return fmt.Errorf("initialize data source: %w", err)
	}
	defer func() {
		logger.Debug("Closing data source...")
		if closeErr := postgresSource.Close(ctx); closeErr != nil {
			logger.Warn("Error closing data source", slog.String("error", closeErr.Error()))
		} else {
			logger.Debug("Data source closed successfully.")
		}
	}()

	// --- Report Generation ---
	logger.Info("Initializing report generator...")
	generator := report.NewGenerator(postgresSource, cfg.Report, logger)

	logger.Info("Starting report generation...")
	startTime := time.Now()

	generationCtx, cancelGeneration := context.WithTimeout(ctx, cfg.Report.Timeout)
	defer cancelGeneration()

	err = generator.GenerateReport(generationCtx)
	if err != nil {
		duration := time.Since(startTime)
		// Handle specific context errors for clearer messages.
		if errors.Is(err, context.DeadlineExceeded) {
			errMsg := fmt.Sprintf("report generation timed out after %s", cfg.Report.Timeout)
			logger.Error(errMsg, slog.Duration("duration", duration))
			return errors.New(errMsg)
		}
		if errors.Is(err, context.Canceled) {
			// Could be SIGINT/SIGTERM or parent context cancellation.
			logger.Warn("Report generation cancelled", slog.Duration("duration", duration))
			return errors.New("report generation cancelled")
		}

		logger.Error("Report generation failed", slog.String("error", err.Error()), slog.Duration("duration", duration))
		return fmt.Errorf("report generation: %w", err) // Wrap original error
	}

	// --- Success ---
	duration := time.Since(startTime)
	logger.Info("Report generated successfully",
		slog.String("output_path", cfg.Report.OutputPath),
		slog.Duration("duration", duration),
	)

	return nil
}

func maskDSNPassword(dsn string) string {
	// Example: postgres://user:password@host:port/database?options
	parts := strings.SplitN(dsn, "://", 2)
	if len(parts) != 2 {
		return dsn // Not a standard URL-like DSN
	}
	scheme := parts[0]
	rest := parts[1]

	userInfoHost := strings.SplitN(rest, "@", 2)
	if len(userInfoHost) != 2 {
		return dsn // No user info part
	}
	userInfo := userInfoHost[0]
	hostPath := userInfoHost[1]

	userPass := strings.SplitN(userInfo, ":", 2)
	if len(userPass) != 2 {
		// Only user, no password
		return fmt.Sprintf("%s://%s@%s", scheme, userInfo, hostPath)
	}

	user := userPass[0]
	return fmt.Sprintf("%s://%s:********@%s", scheme, user, hostPath)
}
