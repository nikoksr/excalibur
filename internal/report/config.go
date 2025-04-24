package report

import (
	"context"
	"os"
	"regexp"
	"time"
)

// excelColRegex validates standard Excel column names (e.g., A, Z, AA, XFD).
var excelColumnRegex = regexp.MustCompile(`^[A-Z]+$`)

type Config struct {
	TemplatePath        string        // Path (relative or absolute) to the input Excel template file.
	DataSourceRefColumn string        // Column letter indicating the data source reference (SQL path). Case-insensitive input, stored uppercase.
	QueriesDir          string        // Base directory relative to the template file for resolving SQL file paths.
	OutputPath          string        // Absolute path where the generated report will be saved.
	Timeout             time.Duration // Maximum duration for the report generation process.
}

// nolint: goconst // Won't const config validation messages
func (c Config) Valid(_ context.Context) map[string]string {
	problems := make(map[string]string)

	if c.TemplatePath == "" {
		problems["template_path"] = "must not be empty"
	} else if !doesPathExist(c.TemplatePath) {
		problems["template_path"] = "path does not exist"
	}

	if c.DataSourceRefColumn == "" {
		problems["data_source_ref_column"] = "must not be empty"
	} else if !isValidColumnName(c.DataSourceRefColumn) {
		problems["data_source_ref_column"] = "must be a valid Excel column name (e.g., A, B, C, ..., Z, AA, AB, ..., XFD)"
	}

	if c.QueriesDir == "" {
		problems["queries_dir"] = "must not be empty"
	} else if !doesPathExist(c.QueriesDir) {
		problems["queries_dir"] = "path does not exist"
	}

	if c.OutputPath == "" {
		problems["output_path"] = "must not be empty"
	}

	// Don't need to check if the output path exists, as we will create it.

	if c.Timeout <= 0 {
		problems["timeout"] = "must be greater than 0"
	}

	return problems
}

func isValidColumnName(column string) bool {
	return excelColumnRegex.MatchString(column)
}

func doesPathExist(path string) bool {
	_, err := os.Stat(path)
	return !os.IsNotExist(err)
}
