package report_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/nikoksr/excalibur/internal/report"
)

func TestReportConfig_Valid(t *testing.T) {
	t.Parallel()

	// Setup temp files/dirs needed for path existence checks
	baseTmpDir := t.TempDir()
	existingQueriesDir := filepath.Join(baseTmpDir, "queries")
	require.NoError(t, os.Mkdir(existingQueriesDir, 0o750))
	existingTemplateFile, err := os.CreateTemp(baseTmpDir, "template-*.xlsx")
	require.NoError(t, err)
	existingTemplatePath := existingTemplateFile.Name()
	existingTemplateFile.Close() // Close the file handle

	// Create a dummy file where a directory is expected, and vice versa
	dummyFilePath := filepath.Join(baseTmpDir, "dummy_file.txt")
	require.NoError(t, os.WriteFile(dummyFilePath, []byte("test"), 0o640))
	dummyDirPath := filepath.Join(baseTmpDir, "dummy_dir")
	require.NoError(t, os.Mkdir(dummyDirPath, 0o750))

	nonExistentPath := filepath.Join(baseTmpDir, "this_does_not_exist")
	dummyOutputPath := filepath.Join(
		baseTmpDir,
		"output.xlsx",
	) // Output doesn't need to exist, just needs to be absolute

	// Base valid config (assuming paths are absolute, which they are from TempDir)
	validBaseCfg := report.Config{
		TemplatePath:        existingTemplatePath,
		DataSourceRefColumn: "A",
		QueriesDir:          existingQueriesDir,
		OutputPath:          dummyOutputPath,
		Timeout:             1 * time.Minute,
	}

	testCases := []struct {
		name                 string
		cfg                  report.Config // Input config to validate
		expectValid          bool
		expectedProblemKey   string // Key expected in the problems map if invalid
		expectedErrSubstring string // Substring expected in the problem message
	}{
		{
			name:        "Valid Config",
			cfg:         validBaseCfg,
			expectValid: true,
		},
		// --- TemplatePath Validations ---
		{
			name: "Missing Template Path",
			cfg: func() report.Config {
				c := validBaseCfg
				c.TemplatePath = ""
				return c
			}(),
			expectValid:          false,
			expectedProblemKey:   "template_path",
			expectedErrSubstring: "must not be empty",
		},
		{
			name: "Relative Template Path", // Should fail as Valid expects absolute
			cfg: func() report.Config {
				c := validBaseCfg
				c.TemplatePath = "relative/path.xlsx"
				return c
			}(),
			expectValid:          false,
			expectedProblemKey:   "template_path",
			expectedErrSubstring: "path must be absolute",
		},
		{
			name: "Non-existent Template Path",
			cfg: func() report.Config {
				c := validBaseCfg
				c.TemplatePath = nonExistentPath
				return c
			}(),
			expectValid:          false,
			expectedProblemKey:   "template_path",
			expectedErrSubstring: "path error: stat", // OS-specific message part
		},
		{
			name: "Template Path is Directory",
			cfg: func() report.Config {
				c := validBaseCfg
				c.TemplatePath = existingQueriesDir // Use the queries dir path
				return c
			}(),
			expectValid:          false,
			expectedProblemKey:   "template_path",
			expectedErrSubstring: "path must be a file, not a directory",
		},
		// --- DataSourceRefColumn Validations ---
		{
			name: "Missing Ref Column",
			cfg: func() report.Config {
				c := validBaseCfg
				c.DataSourceRefColumn = ""
				return c
			}(),
			expectValid:          false,
			expectedProblemKey:   "data_source_ref_column",
			expectedErrSubstring: "must not be empty",
		},
		{
			name: "Invalid Ref Column Format (Number)",
			cfg: func() report.Config {
				c := validBaseCfg
				c.DataSourceRefColumn = "1A" // Invalid format
				return c
			}(),
			expectValid:          false,
			expectedProblemKey:   "data_source_ref_column",
			expectedErrSubstring: "must be a valid Excel column name",
		},
		{
			name: "Invalid Ref Column Format (Lowercase)", // Assumes normalization happens before Valid
			cfg: func() report.Config {
				c := validBaseCfg
				c.DataSourceRefColumn = "aa" // Invalid format for regex
				return c
			}(),
			expectValid:          false,
			expectedProblemKey:   "data_source_ref_column",
			expectedErrSubstring: "must be a valid Excel column name",
		},
		{
			name: "Valid Ref Column (Multi-char)",
			cfg: func() report.Config {
				c := validBaseCfg
				c.DataSourceRefColumn = "XFD" // Valid
				return c
			}(),
			expectValid: true,
		},
		// --- QueriesDir Validations ---
		{
			name: "Missing Queries Dir",
			cfg: func() report.Config {
				c := validBaseCfg
				c.QueriesDir = ""
				return c
			}(),
			expectValid:          false,
			expectedProblemKey:   "queries_dir",
			expectedErrSubstring: "must not be empty",
		},
		{
			name: "Relative Queries Dir", // Should fail as Valid expects absolute
			cfg: func() report.Config {
				c := validBaseCfg
				c.QueriesDir = "relative/queries"
				return c
			}(),
			expectValid:          false,
			expectedProblemKey:   "queries_dir",
			expectedErrSubstring: "path must be absolute",
		},
		{
			name: "Non-existent Queries Dir",
			cfg: func() report.Config {
				c := validBaseCfg
				c.QueriesDir = nonExistentPath
				return c
			}(),
			expectValid:          false,
			expectedProblemKey:   "queries_dir",
			expectedErrSubstring: "path error: stat",
		},
		{
			name: "Queries Dir is File",
			cfg: func() report.Config {
				c := validBaseCfg
				c.QueriesDir = existingTemplatePath // Use the template file path
				return c
			}(),
			expectValid:          false,
			expectedProblemKey:   "queries_dir",
			expectedErrSubstring: "path must be a directory, not a file",
		},
		// --- OutputPath Validations ---
		{
			name: "Missing Output Path",
			cfg: func() report.Config {
				c := validBaseCfg
				c.OutputPath = ""
				return c
			}(),
			expectValid:          false,
			expectedProblemKey:   "output_path",
			expectedErrSubstring: "must not be empty",
		},
		{
			name: "Relative Output Path", // Should fail as Valid expects absolute
			cfg: func() report.Config {
				c := validBaseCfg
				c.OutputPath = "relative/output.xlsx"
				return c
			}(),
			expectValid:          false,
			expectedProblemKey:   "output_path",
			expectedErrSubstring: "path must be absolute",
		},
		// --- Timeout Validations ---
		{
			name: "Zero Timeout",
			cfg: func() report.Config {
				c := validBaseCfg
				c.Timeout = 0
				return c
			}(),
			expectValid:          false,
			expectedProblemKey:   "timeout",
			expectedErrSubstring: "must be a positive duration",
		},
		{
			name: "Negative Timeout",
			cfg: func() report.Config {
				c := validBaseCfg
				c.Timeout = -5 * time.Minute
				return c
			}(),
			expectValid:          false,
			expectedProblemKey:   "timeout",
			expectedErrSubstring: "must be a positive duration",
		},
		// --- Multiple Errors ---
		{
			name: "Multiple Errors",
			cfg: report.Config{ // Empty config essentially
				TemplatePath:        "",
				DataSourceRefColumn: "1",
				QueriesDir:          dummyFilePath, // Is a file
				OutputPath:          "relative/path",
				Timeout:             0,
			},
			expectValid: false,
			// We don't check all errors, just that it's invalid
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			problems := tc.cfg.Valid(context.Background())

			if tc.expectValid {
				assert.Empty(t, problems, "Expected no validation problems")
			} else {
				require.NotEmpty(t, problems, "Expected validation problems")
				if tc.expectedProblemKey != "" {
					assert.Contains(t, problems, tc.expectedProblemKey, "Expected problem key '%s' not found", tc.expectedProblemKey)
					if tc.expectedErrSubstring != "" && problems[tc.expectedProblemKey] != "" {
						assert.Contains(t, problems[tc.expectedProblemKey], tc.expectedErrSubstring, "Problem message for key '%s' did not contain expected substring", tc.expectedProblemKey)
					}
				}
			}
		})
	}
}
