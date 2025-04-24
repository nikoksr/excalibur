package config

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
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

	EnvDSN                    = EnvPrefix + "DSN"
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

	logger.Debug("Normalizing configuration...")

	normalizedCfg := cfg // Operate on a copy

	// Ensure consistent case for column comparison later.
	normalizedCfg.Report.DataSourceRefColumn = strings.ToUpper(normalizedCfg.Report.DataSourceRefColumn)
	logger.Debug(
		"Normalized DataSourceRefColumn to uppercase",
		slog.String("value", normalizedCfg.Report.DataSourceRefColumn),
	)

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
	logger.Debug(
		"Resolved absolute path",
		slog.String("description", description),
		slog.String("original", path),
		slog.String("absolute", absPath),
	)
	return absPath, nil
}
