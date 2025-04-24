package config_test

import (
	"log/slog"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/nikoksr/excalibur/internal/config"
	"github.com/nikoksr/excalibur/internal/datasource"
	"github.com/nikoksr/excalibur/internal/report"
)

func TestValidate(t *testing.T) {
	// Setup temp files/dirs needed for path existence checks
	baseTmpDir := t.TempDir()
	existingQueriesDir := filepath.Join(baseTmpDir, "queries")
	require.NoError(t, os.Mkdir(existingQueriesDir, 0o750))
	existingTemplateFile, err := os.CreateTemp(baseTmpDir, "template-*.xlsx")
	require.NoError(t, err)
	existingTemplatePath := existingTemplateFile.Name()
	existingTemplateFile.Close()
	nonExistentPath := filepath.Join(baseTmpDir, "this_does_not_exist")
	dummyOutputPath := filepath.Join(baseTmpDir, "output.xlsx") // Output doesn't need to exist

	// Base valid config
	validBaseCfg := config.Config{
		DataSource: datasource.Config{DSN: "valid-dsn"},
		Report: report.Config{
			TemplatePath:        existingTemplatePath,
			DataSourceRefColumn: "A",
			QueriesDir:          existingQueriesDir,
			OutputPath:          dummyOutputPath,
			Timeout:             1 * time.Minute,
		},
	}

	testCases := []struct {
		name                 string
		cfg                  config.Config // Input config to validate
		expectErr            bool
		expectedErrSubstring string
	}{
		{
			name:      "Valid Config",
			cfg:       validBaseCfg,
			expectErr: false,
		},
		{
			name: "Missing DSN",
			cfg: func() config.Config {
				c := validBaseCfg
				c.DataSource.DSN = ""
				return c
			}(),
			expectErr:            true,
			expectedErrSubstring: "datasource.dsn: must not be empty",
		},
		{
			name: "Missing Template Path",
			cfg: func() config.Config {
				c := validBaseCfg
				c.Report.TemplatePath = ""
				return c
			}(),
			expectErr:            true,
			expectedErrSubstring: "report.template_path: must not be empty",
		},
		{
			name: "Non-existent Template Path",
			cfg: func() config.Config {
				c := validBaseCfg
				c.Report.TemplatePath = nonExistentPath
				return c
			}(),
			expectErr:            true,
			expectedErrSubstring: "report.template_path: path error",
		},
		{
			name: "Missing Queries Dir",
			cfg: func() config.Config {
				c := validBaseCfg
				c.Report.QueriesDir = ""
				return c
			}(),
			expectErr:            true,
			expectedErrSubstring: "report.queries_dir: must not be empty",
		},
		{
			name: "Non-existent Queries Dir",
			cfg: func() config.Config {
				c := validBaseCfg
				c.Report.QueriesDir = nonExistentPath
				return c
			}(),
			expectErr:            true,
			expectedErrSubstring: "report.queries_dir: path error",
		},
		{
			name: "Missing Output Path",
			cfg: func() config.Config {
				c := validBaseCfg
				c.Report.OutputPath = ""
				return c
			}(),
			expectErr:            true,
			expectedErrSubstring: "report.output_path: must not be empty",
		},
		{
			name: "Missing Ref Column",
			cfg: func() config.Config {
				c := validBaseCfg
				c.Report.DataSourceRefColumn = ""
				return c
			}(),
			expectErr:            true,
			expectedErrSubstring: "report.data_source_ref_column: must not be empty",
		},
		{
			name: "Invalid Ref Column",
			cfg: func() config.Config {
				c := validBaseCfg
				c.Report.DataSourceRefColumn = "1A" // Invalid format
				return c
			}(),
			expectErr:            true,
			expectedErrSubstring: "report.data_source_ref_column: must be a valid Excel column name",
		},
		{
			name: "Zero Timeout",
			cfg: func() config.Config {
				c := validBaseCfg
				c.Report.Timeout = 0
				return c
			}(),
			expectErr:            true,
			expectedErrSubstring: "report.timeout: must be a positive duration",
		},
		{
			name: "Multiple Errors",
			cfg: config.Config{ // Completely empty config
				DataSource: datasource.Config{},
				Report:     report.Config{},
			},
			expectErr:            true,
			expectedErrSubstring: "invalid configuration:", // Check for multiple specific errors if needed
		},
	}

	mutedSlog := slog.New(slog.DiscardHandler)

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			err := config.Validate(t.Context(), tc.cfg, mutedSlog)

			if tc.expectErr {
				require.Error(t, err, "Expected an error but got none")
				if tc.expectedErrSubstring != "" {
					assert.Contains(t, err.Error(), tc.expectedErrSubstring, "Expected error to contain substring")
				}
			} else {
				require.NoError(t, err, "Expected no error but got one: %v", err)
			}
		})
	}
}

func TestNormalize(t *testing.T) {
	// Get current working directory for baseline absolute paths
	cwd, err := os.Getwd()
	require.NoError(t, err, "Failed to get current working directory")

	// Base config with relative paths
	baseRelativeCfg := config.Config{
		DataSource: datasource.Config{DSN: "relative-dsn"}, // DSN not normalized
		Report: report.Config{
			TemplatePath:        "rel_template.xlsx",
			DataSourceRefColumn: "A", // Not normalized
			QueriesDir:          "rel_queries",
			OutputPath:          filepath.Join("rel_output", "report.xlsx"),
			Timeout:             1 * time.Minute, // Not normalized
		},
	}

	// Expected config with absolute paths based on CWD
	expectedNormalizedCfg := config.Config{
		DataSource: datasource.Config{DSN: "relative-dsn"}, // Unchanged
		Report: report.Config{
			TemplatePath:        filepath.Join(cwd, "rel_template.xlsx"),
			DataSourceRefColumn: "A", // Unchanged
			QueriesDir:          filepath.Join(cwd, "rel_queries"),
			OutputPath:          filepath.Join(cwd, "rel_output", "report.xlsx"),
			Timeout:             1 * time.Minute, // Unchanged
		},
	}

	// Config already having absolute paths
	baseAbsoluteCfg := config.Config{
		DataSource: datasource.Config{DSN: "abs-dsn"},
		Report: report.Config{
			TemplatePath:        filepath.Join(cwd, "abs_template.xlsx"), // Already absolute
			DataSourceRefColumn: "B",
			QueriesDir:          filepath.Join(cwd, "abs_queries"),               // Already absolute
			OutputPath:          filepath.Join(cwd, "abs_output", "report.xlsx"), // Already absolute
			Timeout:             2 * time.Minute,
		},
	}

	testCases := []struct {
		name        string
		cfg         config.Config // Input config
		expectErr   bool
		expectedCfg config.Config // Expected normalized config
	}{
		{
			name:        "Relative Paths",
			cfg:         baseRelativeCfg,
			expectErr:   false,
			expectedCfg: expectedNormalizedCfg,
		},
		{
			name:        "Absolute Paths",
			cfg:         baseAbsoluteCfg,
			expectErr:   false,
			expectedCfg: baseAbsoluteCfg, // Should remain unchanged
		},
		{
			name: "Mixed Paths",
			cfg: config.Config{
				DataSource: datasource.Config{DSN: "mixed-dsn"},
				Report: report.Config{
					TemplatePath:        "mixed_template.xlsx", // Relative
					DataSourceRefColumn: "C",
					QueriesDir:          filepath.Join(cwd, "mixed_queries"), // Absolute
					OutputPath:          "mixed_output.xlsx",                 // Relative
					Timeout:             3 * time.Minute,
				},
			},
			expectErr: false,
			expectedCfg: config.Config{
				DataSource: datasource.Config{DSN: "mixed-dsn"},
				Report: report.Config{
					TemplatePath:        filepath.Join(cwd, "mixed_template.xlsx"), // Normalized
					DataSourceRefColumn: "C",
					QueriesDir:          filepath.Join(cwd, "mixed_queries"),     // Unchanged
					OutputPath:          filepath.Join(cwd, "mixed_output.xlsx"), // Normalized
					Timeout:             3 * time.Minute,
				},
			},
		},
		{
			name: "Empty Paths",
			cfg: config.Config{
				DataSource: datasource.Config{DSN: "empty-path-dsn"},
				Report: report.Config{
					TemplatePath:        "", // Empty
					DataSourceRefColumn: "E",
					QueriesDir:          "", // Empty
					OutputPath:          "", // Empty
					Timeout:             1 * time.Minute,
				},
			},
			expectErr: false,
			expectedCfg: config.Config{
				DataSource: datasource.Config{DSN: "empty-path-dsn"},
				Report: report.Config{
					TemplatePath:        cwd, // Expect CWD
					DataSourceRefColumn: "E",
					QueriesDir:          cwd, // Expect CWD
					OutputPath:          cwd, // Expect CWD
					Timeout:             1 * time.Minute,
				},
			},
		},
	}

	mutedSlog := slog.New(slog.DiscardHandler)

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			normalizedCfg, err := config.Normalize(tc.cfg, mutedSlog)

			if tc.expectErr {
				require.Error(t, err, "Expected an error but got none")
			} else {
				require.NoError(t, err, "Expected no error but got one: %v", err)

				if diff := cmp.Diff(tc.expectedCfg, normalizedCfg); diff != "" {
					t.Errorf("Normalized config mismatch (-want +got):\n%s", diff)
				}
			}
		})
	}
}
