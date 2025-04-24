package config

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/nikoksr/assert-go"

	"excalibur/internal/datasource"
	"excalibur/internal/report"
)

type Config struct {
	DataSource datasource.Config
	Report     report.Config
}

const (
	EnvPrefix = "EXCALIBUR_"

	EnvDSN = EnvPrefix + "DSN"

	EnvReportTemplatePath     = EnvPrefix + "REPORT_TEMPLATE_PATH"
	EnvReportDataSourceRefCol = EnvPrefix + "REPORT_DATASOURCE_REF_COL"
	EnvReportQueriesDir       = EnvPrefix + "REPORT_QUERIES_DIR"
	EnvReportOutputPath       = EnvPrefix + "REPORT_OUTPUT_PATH"
	EnvReportTimeout          = EnvPrefix + "REPORT_TIMEOUT"
)

const (
	DefaultReportTimeout    = 5 * time.Minute
	DefaultReportRefColumn  = "R"       // Default Excel column for datasource references.
	DefaultReportQueriesDir = "queries" // Default relative directory for SQL files.
	DefaultReportOutputPath = "excalibur_report.xlsx"
)

func Load(args []string, getenv func(string) string, logger *slog.Logger) (Config, error) {
	assert.Assert(args != nil, "args must not be nil")
	assert.Assert(getenv != nil, "getenv must not be nil")
	assert.Assert(logger != nil, "logger must not be nil")

	cfg := Config{
		Report: report.Config{
			Timeout:             DefaultReportTimeout,
			DataSourceRefColumn: DefaultReportRefColumn,
			QueriesDir:          DefaultReportQueriesDir,
			OutputPath:          DefaultReportOutputPath,
		},
	}

	// Use a dedicated flag set to avoid interfering with the global one (e.g., -verbose).
	fs := flag.NewFlagSet("excalibur", flag.ContinueOnError)
	fs.SetOutput(io.Discard)

	// --- Register Flags ---

	// DataSource Flags
	fs.StringVar(&cfg.DataSource.DSN, "dsn", getenvOrDefault(getenv, EnvDSN, ""),
		"DSN for the data source (e.g., postgresql://user:pass@host:port/db). (Env: "+EnvDSN+")")

	// Report Flags
	fs.StringVar(&cfg.Report.TemplatePath, "report-template-path", getenvOrDefault(getenv, EnvReportTemplatePath, ""),
		"Path to the input Excel template file (.xlsx). (Env: "+EnvReportTemplatePath+")")
	fs.StringVar(
		&cfg.Report.DataSourceRefColumn,
		"report-ref-col",
		getenvOrDefault(getenv, EnvReportDataSourceRefCol, DefaultReportRefColumn),
		fmt.Sprintf("Excel column containing the SQL file reference (e.g., 'Q'). (Env: %s)", EnvReportDataSourceRefCol),
	)
	fs.StringVar(
		&cfg.Report.QueriesDir,
		"report-queries-dir",
		getenvOrDefault(getenv, EnvReportQueriesDir, DefaultReportQueriesDir),
		"Directory containing SQL query files, relative to the template or absolute. (Env: "+EnvReportQueriesDir+")",
	)
	fs.StringVar(
		&cfg.Report.OutputPath,
		"report-output-path",
		getenvOrDefault(getenv, EnvReportOutputPath, DefaultReportOutputPath),
		"Path where the generated Excel report will be saved. (Env: "+EnvReportOutputPath+")",
	)

	// Report Timeout Flag (Duration)
	// Parse env var first, fallback to default, then register flag using that as the flag's default.
	defaultTimeoutStr := DefaultReportTimeout.String()
	envTimeoutStr := getenvOrDefault(getenv, EnvReportTimeout, defaultTimeoutStr)
	parsedTimeoutFromEnv, err := time.ParseDuration(envTimeoutStr)
	if err != nil {
		logger.Warn(
			"Invalid duration format in environment variable, using default",
			slog.String("env_var", EnvReportTimeout),
			slog.String("value", envTimeoutStr),
			slog.String("default", defaultTimeoutStr),
			slog.String("error", err.Error()),
		)
		parsedTimeoutFromEnv = DefaultReportTimeout // Fallback
	}
	fs.DurationVar(&cfg.Report.Timeout, "report-timeout", parsedTimeoutFromEnv,
		fmt.Sprintf("Maximum duration for report generation (e.g., '5m', '1h30m'). (Env: %s)", EnvReportTimeout))

	// --- Parse ---
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			// Print usage if help was requested.
			fmt.Fprintf(os.Stderr, "Usage of Excalibur:\n")
			fs.PrintDefaults()
			return Config{}, flag.ErrHelp // Propagate ErrHelp for clean exit in main
		}
		logger.Error("Error parsing command-line flags", slog.String("error", err.Error()))
		return Config{}, fmt.Errorf("parsing flags: %w", err)
	}

	// --- Post-processing ---
	// Ensure consistent case for column comparison later.
	cfg.Report.DataSourceRefColumn = strings.ToUpper(cfg.Report.DataSourceRefColumn)
	logger.Debug("Configuration loaded (raw)", slog.Any("config", cfg))

	return cfg, nil
}

func Validate(ctx context.Context, cfg Config, logger *slog.Logger) error {
	assert.Assert(ctx != nil, "context must not be nil")
	assert.Assert(logger != nil, "logger must not be nil")

	logger.Debug("Validating configuration rules...")

	validationProblems := make(map[string]string)
	datasourceProblems := cfg.DataSource.Valid(ctx)
	for key, problem := range datasourceProblems {
		validationProblems["datasource."+key] = problem
	}

	reportProblems := cfg.Report.Valid(ctx)
	for key, problem := range reportProblems {
		validationProblems["report."+key] = problem
	}

	if len(validationProblems) > 0 {
		var errBuilder strings.Builder
		errBuilder.WriteString("invalid configuration:")
		for key, problem := range validationProblems {
			errBuilder.WriteString(fmt.Sprintf("\n - %s: %s", key, problem))
			logger.Debug("Validation issue", slog.String("field", key), slog.String("problem", problem))
		}
		return errors.New(errBuilder.String())
	}

	logger.Debug("Configuration validation successful.")
	return nil
}

func Normalize(cfg Config, logger *slog.Logger) (Config, error) {
	assert.Assert(logger != nil, "logger must not be nil")

	logger.Debug("Normalizing configuration paths...")

	normalizedCfg := cfg // Operate on a copy

	var err error
	normalizedCfg.Report.TemplatePath, err = makeAbsolutePath(
		normalizedCfg.Report.TemplatePath,
		"template path",
		logger,
	)
	if err != nil {
		return Config{}, err
	}

	normalizedCfg.Report.OutputPath, err = makeAbsolutePath(normalizedCfg.Report.OutputPath, "output path", logger)
	if err != nil {
		return Config{}, err
	}

	normalizedCfg.Report.QueriesDir, err = makeAbsolutePath(
		normalizedCfg.Report.QueriesDir,
		"queries directory",
		logger,
	)
	if err != nil {
		return Config{}, err
	}

	logger.Debug("Configuration normalization successful.")
	return normalizedCfg, nil
}

func makeAbsolutePath(path, description string, logger *slog.Logger) (string, error) {
	absPath, err := filepath.Abs(path)
	if err != nil {
		logger.Error(
			"Failed to get absolute path",
			slog.String("description", description),
			slog.String("path", path),
			slog.String("error", err.Error()),
		)
		return "", fmt.Errorf("normalize %s %q: %w", description, path, err)
	}

	return absPath, nil
}

// getenvOrDefault retrieves an environment variable or returns a default value if empty.
func getenvOrDefault(getenv func(string) string, key string, defaultValue string) string {
	value := getenv(key)
	if value == "" {
		return defaultValue
	}

	return value
}
