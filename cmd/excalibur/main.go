package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	cliapp "github.com/nikoksr/excalibur/internal/cli"
	"github.com/nikoksr/excalibur/internal/config"
	"github.com/nikoksr/excalibur/internal/datasource"
	"github.com/nikoksr/excalibur/internal/report"
)

var version = "dev" // Will be set by the build system

func main() {
	app := cliapp.NewApp(version, runExcalibur)

	err := app.Run(context.Background(), os.Args)
	if err != nil {
		fmt.Fprintln(os.Stderr, "Error:", err)
		os.Exit(1)
	}
}

func runExcalibur(ctx context.Context, cfg *config.Config, logger *slog.Logger) error {
	// Context with signal handling for graceful shutdown
	runCtx, stop := signal.NotifyContext(ctx, syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	logger.Info("Starting Excalibur Core Logic")

	logger.Debug("Using validated and normalized configuration",
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

	logger.Debug("Full DSN", slog.String("dsn", cfg.DataSource.DSN))

	// --- Datasource Setup ---
	logger.Info("Initializing data source...")
	postgresSource, err := datasource.NewPostgresDataSource(runCtx, cfg.DataSource, logger)
	if err != nil {
		logger.Error("Failed to initialize data source", slog.String("error", err.Error()))
		return fmt.Errorf("initialize data source: %w", err) // Return error to CLI Action
	}
	defer func() {
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		logger.Debug("Closing data source...")
		if closeErr := postgresSource.Close(cleanupCtx); closeErr != nil {
			logger.Warn("Error closing data source", slog.String("error", closeErr.Error()))
		}
	}()

	// --- Report Generation ---
	logger.Info("Initializing report generator...")
	generator := report.NewGenerator(postgresSource, cfg.Report, logger)

	logger.Info("Starting report generation...")
	startTime := time.Now()

	generationCtx, cancelGeneration := context.WithTimeout(runCtx, cfg.Report.Timeout)
	defer cancelGeneration()

	err = generator.GenerateReport(generationCtx)
	duration := time.Since(startTime)

	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) {
			errMsg := fmt.Sprintf("report generation timed out after %s", cfg.Report.Timeout)
			logger.Error(errMsg, slog.Duration("duration", duration))
			return errors.New(errMsg) // Return error to CLI Action
		}
		if errors.Is(err, context.Canceled) {
			if errors.Is(runCtx.Err(), context.Canceled) {
				logger.Warn("Report generation cancelled by signal", slog.Duration("duration", duration))
				return errors.New("report generation cancelled by signal")
			}

			logger.Warn(
				"Report generation cancelled",
				slog.Duration("duration", duration),
				slog.String("reason", err.Error()),
			)
			return fmt.Errorf("report generation cancelled: %w", err)
		}

		logger.Error("Report generation failed", slog.String("error", err.Error()), slog.Duration("duration", duration))
		return fmt.Errorf("report generation: %w", err) // Wrap and return original error
	}

	// --- Success ---
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
