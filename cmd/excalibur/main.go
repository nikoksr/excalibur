package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"

	"excalibur/internal/config"
	"excalibur/internal/datasource"
	"excalibur/internal/report"
)

func main() {
	logger := slog.Default()

	if err := run(os.Args[1:], os.Getenv, logger); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			os.Exit(0)
		}

		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}

func run(args []string, getenv func(string) string, logger *slog.Logger) error {
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	// Load Raw Config
	cfgRaw, err := config.Load(args, getenv)
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}

	// Validate Config
	if err = config.Validate(ctx, cfgRaw); err != nil {
		return fmt.Errorf("validate config: %w", err)
	}

	// Normalize Config
	cfg, err := config.Normalize(cfgRaw)
	if err != nil {
		return fmt.Errorf("normalize config: %w", err)
	}

	logger.Debug("Using normalized configuration",
		slog.String("template", cfg.Report.TemplatePath),
		slog.String("output", cfg.Report.OutputPath),
		slog.String("queries", cfg.Report.QueriesDir),
	)

	// Setup datasource
	logger.Info("Initializing data source...")

	postgresSource, err := datasource.NewPostgresDataSource(ctx, cfg.DataSource)
	if err != nil {
		return fmt.Errorf("initialize data source: %w", err)
	}
	defer func() {
		if closeErr := postgresSource.Close(ctx); closeErr != nil {
			logger.Warn("Error closing data source", slog.String("error", closeErr.Error()))
		}
	}()

	// Setup report generator
	logger.Info("Initializing report generator...")
	generator := report.NewGenerator(postgresSource, cfg.Report, logger)

	// Generate the report
	logger.Info("Starting report generation...")
	startTime := time.Now()

	generationCtx, cancelGeneration := context.WithTimeout(ctx, cfg.Report.Timeout)
	defer cancelGeneration()

	err = generator.GenerateReport(generationCtx)
	if errors.Is(err, context.DeadlineExceeded) {
		return fmt.Errorf("report generation timed out after %s", cfg.Report.Timeout)
	}
	if errors.Is(err, context.Canceled) {
		return errors.New("report generation cancelled")
	}
	if err != nil {
		return err
	}

	// Done
	duration := time.Since(startTime)
	logger.Info("Report generated successfully",
		slog.String("output_path", cfg.Report.OutputPath),
		slog.Duration("duration", duration),
	)

	return nil
}
