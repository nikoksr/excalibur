// nolint: goconst // Validation messages don't need to be constants.
package report

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"time"
)

// excelColumnRegex validates standard Excel column names (e.g., A, Z, AA, XFD).
var excelColumnRegex = regexp.MustCompile(`^[A-Z]+$`)

type Config struct {
	TemplatePath        string        // Absolute path to the input Excel template file (.xlsx).
	DataSourceRefColumn string        // Uppercase Excel column letter indicating the SQL file reference (e.g., "R").
	QueriesDir          string        // Absolute base directory for resolving SQL file paths found in the reference column.
	OutputPath          string        // Absolute path where the generated report will be saved.
	Timeout             time.Duration // Maximum duration allowed for the entire report generation process.
}

func (c Config) Valid(_ context.Context) map[string]string {
	problems := make(map[string]string)

	// Validate TemplatePath
	if c.TemplatePath == "" {
		problems["template_path"] = "must not be empty"
	} else if !filepath.IsAbs(c.TemplatePath) {
		problems["template_path"] = "path must be absolute (normalization likely failed)"
	} else if fi, err := os.Stat(c.TemplatePath); err != nil {
		problems["template_path"] = fmt.Sprintf("path error: %v", err)
	} else if fi.IsDir() {
		problems["template_path"] = "path must be a file, not a directory"
	}

	// Validate DataSourceRefColumn
	if c.DataSourceRefColumn == "" {
		problems["data_source_ref_column"] = "must not be empty"
	} else if !isValidExcelColumnName(c.DataSourceRefColumn) {
		// Assumes ToUpper was done during Load.
		problems["data_source_ref_column"] = fmt.Sprintf("must be a valid Excel column name (A-XFD), got: %s", c.DataSourceRefColumn)
	}

	// Validate QueriesDir
	if c.QueriesDir == "" {
		problems["queries_dir"] = "must not be empty"
	} else if !filepath.IsAbs(c.QueriesDir) {
		problems["queries_dir"] = "path must be absolute (normalization likely failed)"
	} else if fi, err := os.Stat(c.QueriesDir); err != nil {
		problems["queries_dir"] = fmt.Sprintf("path error: %v", err)
	} else if !fi.IsDir() {
		problems["queries_dir"] = "path must be a directory, not a file"
	}

	// Validate OutputPath
	if c.OutputPath == "" {
		problems["output_path"] = "must not be empty"
	} else if !filepath.IsAbs(c.OutputPath) {
		problems["output_path"] = "path must be absolute (normalization likely failed)"
	}

	// Validate Timeout
	if c.Timeout <= 0 {
		problems["timeout"] = "must be a positive duration"
	}

	return problems
}

func isValidExcelColumnName(column string) bool {
	return excelColumnRegex.MatchString(column)
}
