package config

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/nikoksr/assert-go"

	"excalibur/internal/datasource"
	"excalibur/internal/report"
)

type Config struct {
	DataSource datasource.Config // Configuration for the data source (e.g., postgres).
	Report     report.Config     // Configuration for report generation.
}

// --- Constants for Environment Variable Names ---.
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
	DefaultReportRefColumn  = "R"
	DefaultReportQueriesDir = "queries"
	DefaultReportOutputPath = "excalibur_report.xlsx"
)

func Load(args []string, getenv func(string) string) (Config, error) {
	assert.Assert(args != nil, "args must not be nil")
	assert.Assert(getenv != nil, "getenv must not be nil")

	// Initialize with zero values or potential hardcoded defaults
	cfg := Config{
		Report: report.Config{
			Timeout: DefaultReportTimeout,
		},
	}

	fs := flag.NewFlagSet("excalibur", flag.ContinueOnError)

	// --- Register Flags (as before) ---
	fs.StringVar(
		&cfg.DataSource.DSN,
		"dsn",
		getenvOrDefault(getenv, EnvDSN, ""),
		"[REQUIRED] DSN for the data source (e.g., postgres).",
	)
	fs.StringVar(
		&cfg.Report.TemplatePath,
		"report-template-path",
		getenvOrDefault(getenv, EnvReportTemplatePath, ""),
		"[REQUIRED] Path to the report template file.",
	)
	fs.StringVar(
		&cfg.Report.DataSourceRefColumn,
		"report-ref-col",
		getenvOrDefault(getenv, EnvReportDataSourceRefCol, DefaultReportRefColumn),
		"Name of the Excel column that contains the data source reference.",
	)
	fs.StringVar(
		&cfg.Report.QueriesDir,
		"report-queries-dir",
		getenvOrDefault(getenv, EnvReportQueriesDir, DefaultReportQueriesDir),
		"Path to the directory containing SQL queries for the report.",
	)
	fs.StringVar(
		&cfg.Report.OutputPath,
		"report-output-path",
		getenvOrDefault(getenv, EnvReportOutputPath, DefaultReportOutputPath),
		"Path to the output report file.",
	)

	// Handle duration parsing carefully (still belongs in Load as it's parsing input)
	defaultTimeoutStr := DefaultReportTimeout.String()
	envTimeout := getenvOrDefault(getenv, EnvReportTimeout, defaultTimeoutStr)
	parsedTimeout, err := time.ParseDuration(envTimeout)
	if err != nil {
		return Config{}, fmt.Errorf(
			"invalid duration format for report timeout (%s=%q): %w",
			EnvReportTimeout,
			envTimeout,
			err,
		)
	}
	fs.DurationVar(
		&cfg.Report.Timeout,
		"report-timeout",
		parsedTimeout,
		"Timeout for report generation. Default is 5 minutes.",
	)

	// --- Parse the arguments ---
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return Config{}, err
		}
		return Config{}, fmt.Errorf("error parsing flags: %w", err)
	}

	// --- Post-processing (Simple transformations like ToUpper belong here) ---
	cfg.Report.DataSourceRefColumn = strings.ToUpper(cfg.Report.DataSourceRefColumn)

	return cfg, nil
}

func Validate(ctx context.Context, cfg Config) error {
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
		}
		return errors.New(errBuilder.String())
	}

	return nil
}

func Normalize(cfg Config) (Config, error) {
	normalizedCfg := cfg

	// Normalize paths to be absolute
	absTemplatePath, err := filepath.Abs(normalizedCfg.Report.TemplatePath)
	if err != nil {
		return Config{}, fmt.Errorf("normalize template path %q: %w", normalizedCfg.Report.TemplatePath, err)
	}
	normalizedCfg.Report.TemplatePath = absTemplatePath

	absOutputPath, err := filepath.Abs(normalizedCfg.Report.OutputPath)
	if err != nil {
		return Config{}, fmt.Errorf("normalize output path %q: %w", normalizedCfg.Report.OutputPath, err)
	}
	normalizedCfg.Report.OutputPath = absOutputPath

	absQueriesDir, err := filepath.Abs(normalizedCfg.Report.QueriesDir)
	if err != nil {
		return Config{}, fmt.Errorf("normalize queries dir %q: %w", normalizedCfg.Report.QueriesDir, err)
	}
	normalizedCfg.Report.QueriesDir = absQueriesDir

	return normalizedCfg, nil
}

func getenvOrDefault(getenv func(string) string, key string, defaultValue string) string {
	value := getenv(key)
	if value == "" {
		return defaultValue
	}
	return value
}
