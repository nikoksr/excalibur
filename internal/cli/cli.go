package cli

import (
	"context"
	"fmt"
	"log/slog"
	"os"

	"github.com/nikoksr/assert-go"
	"github.com/urfave/cli/v3"

	"github.com/nikoksr/excalibur/internal/config"
	"github.com/nikoksr/excalibur/internal/logging"
)

type RunFn func(ctx context.Context, cfg *config.Config, logger *slog.Logger) error

func NewApp(version string, runner RunFn) *cli.Command {
	var appConfig config.Config
	var logger *slog.Logger

	rootCmd := &cli.Command{
		Name:    "excalibur",
		Usage:   "Generates Excel reports by executing SQL queries defined within a template.",
		Version: version,
		Flags: []cli.Flag{
			// --- Logging Flag ---
			&cli.BoolFlag{
				Name:  "verbose",
				Usage: "Enable verbose (debug) logging.",
				Sources: cli.NewValueSourceChain(
					cli.EnvVar(config.EnvPrefix + "VERBOSE"),
				), // Allow EXCALIBUR_VERBOSE=true
				Value: false,
			},

			// --- DataSource Flags ---
			&cli.StringFlag{
				Name:     "dsn",
				Usage:    "DSN for the data source (e.g., postgresql://user:pass@host:port/db).",
				Sources:  cli.NewValueSourceChain(cli.EnvVar(config.EnvDSN)), // Env: EXCALIBUR_DSN
				Required: true,                                               // DSN is essential
				// No Value field means it's required unless sourced from EnvVar
			},

			// --- Report Flags ---
			&cli.StringFlag{
				Name:  "report-template-path",
				Usage: "Path to the input Excel template file (.xlsx).",
				Sources: cli.NewValueSourceChain(
					cli.EnvVar(config.EnvReportTemplatePath),
				), // Env: EXCALIBUR_REPORT_TEMPLATE_PATH
				Required: true,
				// No Value field means it's required unless sourced from EnvVar
			},
			&cli.StringFlag{
				Name:  "report-ref-col",
				Usage: "Excel column containing the SQL file reference (e.g., 'Q').",
				Sources: cli.NewValueSourceChain(
					cli.EnvVar(config.EnvReportDataSourceRefCol),
				), // Env: EXCALIBUR_REPORT_DATASOURCE_REF_COL
				Value: config.DefaultReportRefColumn, // Default: "R"
			},
			&cli.StringFlag{
				Name:  "report-queries-dir",
				Usage: "Directory containing SQL query files, relative to the template or absolute.",
				Sources: cli.NewValueSourceChain(
					cli.EnvVar(config.EnvReportQueriesDir),
				), // Env: EXCALIBUR_REPORT_QUERIES_DIR
				Value: config.DefaultReportQueriesDir, // Default: "queries"
			},
			&cli.StringFlag{
				Name:  "report-output-path",
				Usage: "Path where the generated Excel report will be saved.",
				Sources: cli.NewValueSourceChain(
					cli.EnvVar(config.EnvReportOutputPath),
				), // Env: EXCALIBUR_REPORT_OUTPUT_PATH
				Value: config.DefaultReportOutputPath, // Default: "excalibur_report.xlsx"
			},
			&cli.DurationFlag{
				Name:    "report-timeout",
				Usage:   "Maximum duration for report generation (e.g., '5m', '1h30m').",
				Sources: cli.NewValueSourceChain(cli.EnvVar(config.EnvReportTimeout)), // Env: EXCALIBUR_REPORT_TIMEOUT
				Value:   config.DefaultReportTimeout,                                  // Default: 5m
			},
		},
		Before: func(ctx context.Context, cmd *cli.Command) (context.Context, error) {
			verbose := cmd.Bool("verbose")
			logger = logging.NewLogger(os.Stdout, verbose)
			assert.Assert(logger != nil, "Logger must not be nil")

			if verbose {
				logger.Debug("Verbose logging enabled")
			}

			return ctx, nil
		},
		Action: func(ctx context.Context, cmd *cli.Command) error {
			assert.Assert(logger != nil, "Logger must not be nil")

			// --- Populate Config from Flags ---
			logger.Debug("Populating configuration from flags/env...")
			appConfig.DataSource.DSN = cmd.String("dsn")
			appConfig.Report.TemplatePath = cmd.String("report-template-path")
			appConfig.Report.DataSourceRefColumn = cmd.String("report-ref-col")
			appConfig.Report.QueriesDir = cmd.String("report-queries-dir")
			appConfig.Report.OutputPath = cmd.String("report-output-path")
			appConfig.Report.Timeout = cmd.Duration("report-timeout")

			// --- Validate Configuration ---
			logger.Debug("Validating configuration...")
			if err := config.Validate(ctx, appConfig, logger); err != nil {
				logger.Error("Configuration validation failed", slog.String("error", err.Error()))
				return fmt.Errorf("validate configuration: %w", err)
			}

			// --- Normalize Configuration ---
			logger.Debug("Normalizing configuration...")
			normalizedCfg, err := config.Normalize(appConfig, logger)
			if err != nil {
				logger.Error("Configuration normalization failed", slog.String("error", err.Error()))
				return fmt.Errorf("normalize configuration: %w", err)
			}

			// --- Run Core Application Logic ---
			logger.Debug("Configuration loaded and processed, executing core application logic.")
			if err := runner(ctx, &normalizedCfg, logger); err != nil {
				logger.Error("Application execution failed", slog.String("error", err.Error()))
				return err
			}

			logger.Debug("Application finished successfully.")
			return nil
		},
	}

	return rootCmd
}
