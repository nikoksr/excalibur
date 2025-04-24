package report

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"strings"
	"text/template"

	"github.com/nikoksr/assert-go"
	"github.com/xuri/excelize/v2"

	"excalibur/internal/datasource"
)

type Generator struct {
	dataSource datasource.DataSource
	config     Config // Expects normalized config with absolute paths
	logger     *slog.Logger
}

func NewGenerator(source datasource.DataSource, cfg Config, logger *slog.Logger) *Generator {
	assert.Assert(source != nil, "DataSource must not be nil")
	assert.Assert(logger != nil, "Logger must not be nil")
	assert.Assert(filepath.IsAbs(cfg.TemplatePath), "template path must be absolute")
	assert.Assert(filepath.IsAbs(cfg.OutputPath), "output path must be absolute")
	assert.Assert(filepath.IsAbs(cfg.QueriesDir), "queries directory must be absolute")

	return &Generator{
		dataSource: source,
		config:     cfg,
		logger:     logger.With(slog.String("component", "Generator")),
	}
}

func (g *Generator) GenerateReport(ctx context.Context) error {
	g.logger.Info(
		"Starting report generation",
		slog.String("template", g.config.TemplatePath),
		slog.String("output", g.config.OutputPath),
	)

	// 1. Copy Template File to Output Path (Paths are already absolute)
	g.logger.Debug(
		"Copying template file",
		slog.String("from", g.config.TemplatePath),
		slog.String("to", g.config.OutputPath),
	)
	if err := copyFile(g.config.TemplatePath, g.config.OutputPath); err != nil {
		// Wrap error for more context
		return fmt.Errorf("copy template file from %q to %q: %w", g.config.TemplatePath, g.config.OutputPath, err)
	}

	// 2. Open the *copied* Excel file for processing (Path is already absolute)
	g.logger.Debug("Opening copied report file for editing", slog.String("path", g.config.OutputPath))
	f, err := excelize.OpenFile(g.config.OutputPath)
	if err != nil {
		return fmt.Errorf("open copied report file %q: %w", g.config.OutputPath, err)
	}
	defer func() {
		if closeErr := f.Close(); closeErr != nil {
			g.logger.Warn(
				"Error closing report file",
				slog.String("path", g.config.OutputPath),
				slog.String("error", closeErr.Error()),
			)
		}
	}()

	// 3. Prepare for Processing
	sheetList := f.GetSheetList()
	if len(sheetList) == 0 {
		return fmt.Errorf("template file %q contains no sheets", g.config.TemplatePath)
	}

	// Get the 1-based index for the SQL reference column
	sqlColIndex, err := excelize.ColumnNameToNumber(g.config.DataSourceRefColumn)
	if err != nil {
		return fmt.Errorf("internal error: invalid DataSourceRefCol %q: %w", g.config.DataSourceRefColumn, err)
	}

	// Convert to 0-based index for slice access
	zeroBasedSQLColIndex := sqlColIndex - 1

	// --- Determine Query Base Directory ---
	queryBaseDir := g.config.QueriesDir // Use the already absolute path
	g.logger.Debug("Using query base directory", slog.String("dir", queryBaseDir))

	// 4. Process Sheets and Rows
	for _, sheetName := range sheetList {
		// Pass the absolute queryBaseDir down
		if err := g.processSheet(ctx, f, sheetName, zeroBasedSQLColIndex, queryBaseDir); err != nil {
			return fmt.Errorf("processing sheet %q: %w", sheetName, err)
		}
		// Check context after processing each sheet for faster interruption
		if err := ctx.Err(); err != nil {
			return fmt.Errorf("interrupted after processing sheet %q: %w", sheetName, err)
		}
	}

	// 5. Save the final report
	// Update formulas and links before saving. Important for formulas relying on generated data.
	g.logger.Debug("Updating linked values and formulas...")
	if err := f.UpdateLinkedValue(); err != nil {
		return fmt.Errorf("update linked values: %w", err)
	}

	g.logger.Info("Saving generated report...", slog.String("path", g.config.OutputPath))
	if err := f.Save(); err != nil {
		return fmt.Errorf("save the generated report file %q: %w", g.config.OutputPath, err)
	}

	return nil
}

func (g *Generator) processSheet(
	ctx context.Context,
	file *excelize.File,
	sheetName string,
	zeroBasedSQLColIndex int,
	queryBaseDir string,
) error {
	g.logger.Info("Processing sheet", slog.String("sheet_name", sheetName))

	rows, err := file.GetRows(sheetName)
	if err != nil {
		return fmt.Errorf("get rows from sheet %q: %w", sheetName, err)
	}

	g.logger.Debug("Sheet contains rows", slog.String("sheet_name", sheetName), slog.Int("row_count", len(rows)))

	// Process each row
	for rowIndex, rowCells := range rows {
		excelRowIndex := rowIndex + 1 // Excel rows are 1-based

		// Check context at the start of each row processing
		select {
		case <-ctx.Done():
			return fmt.Errorf(
				"processing interrupted on sheet %q, before row %d: %w",
				sheetName,
				excelRowIndex,
				ctx.Err(),
			)
		default:
		}

		// Pass the absolute queryBaseDir down
		if err := g.processRow(ctx, file, sheetName, excelRowIndex, rowCells, zeroBasedSQLColIndex, queryBaseDir); err != nil {
			// Add row context to the error
			return fmt.Errorf("processing row %d: %w", excelRowIndex, err)
		}
	}

	return nil
}

func (g *Generator) processRow(
	ctx context.Context,
	file *excelize.File,
	sheetName string,
	excelRowIndex int,
	rowCells []string,
	zeroBasedSQLColIndex int,
	queryBaseDir string,
) error {
	// Skip if row doesn't have enough columns or the reference cell is empty/whitespace
	if len(rowCells) <= zeroBasedSQLColIndex || strings.TrimSpace(rowCells[zeroBasedSQLColIndex]) == "" {
		return nil
	}

	sqlFilePathRelative := strings.TrimSpace(rowCells[zeroBasedSQLColIndex])
	// Construct the absolute path using the provided absolute queryBaseDir
	sqlFilePath := filepath.Join(queryBaseDir, sqlFilePathRelative)
	// Clean the path (removes redundant separators like //, resolves ., .. etc.)
	sqlFilePath = filepath.Clean(sqlFilePath)

	l := g.logger.With(
		slog.String("sheet", sheetName),
		slog.Int("row", excelRowIndex),
		slog.String("sql_file_relative", sqlFilePathRelative),
		slog.String("sql_file_absolute", sqlFilePath), // Log the final path being used
	)
	l.Info("Found SQL reference, processing row")

	// Clear the SQL reference cell in the output file
	sqlColNum := zeroBasedSQLColIndex + 1
	sqlCellAxis, err := excelize.CoordinatesToCellName(sqlColNum, excelRowIndex)
	if err == nil {
		// SetCellValue to nil effectively clears it
		if err = file.SetCellValue(sheetName, sqlCellAxis, nil); err != nil {
			// Log warning but don't necessarily fail the whole process for this
			l.Warn(
				"Failed to clear SQL reference cell",
				slog.String("cell", sqlCellAxis),
				slog.String("error", err.Error()),
			)
		}
	} else {
		// This is an internal logic error if it happens
		l.Error("Internal error: Failed to calculate SQL reference cell coordinates", slog.String("error", err.Error()))
	}

	// Read the SQL query file
	l.Debug("Attempting to read SQL file") // Added debug log
	queryBytes, err := os.ReadFile(sqlFilePath)
	if err != nil {
		if os.IsNotExist(err) {
			return fmt.Errorf("referenced SQL file not found at %q", sqlFilePath)
		}

		return fmt.Errorf("read SQL file %q: %w", sqlFilePath, err)
	}

	query := string(queryBytes)
	if strings.TrimSpace(query) == "" {
		l.Warn("Skipping row processing: SQL file is empty")
		return nil // Treat empty file as skippable
	}

	// Fetch data from the data source
	l.Debug("Fetching data from data source")
	dataMap, err := g.dataSource.FetchData(ctx, query)
	if err != nil {
		// Log the error, decide if it's fatal or skippable
		// For now, let's warn and skip replacement, but this could be made fatal
		l.Warn(
			"Failed to fetch data from data source, skipping replacements for this row",
			slog.String("error", err.Error()),
		)
		// Depending on requirements, you might want to return an error here:
		// return fmt.Errorf("fetch data using query from %q: %w", sqlFilePath, err)
		return nil // Current behavior: skip replacements if fetch fails
	}

	// Check if any data was actually returned
	if len(dataMap) == 0 {
		l.Warn("Skipping marker replacement for row: Fetched data map is empty.")
		return nil // No data to replace placeholders with
	}
	l.Debug("Data fetched successfully", slog.Any("data_keys", mapsKeys(dataMap))) // Log keys for debugging

	// Iterate through cells in *this* row to find and process templates/placeholders
	for cIdx, originalCellValue := range rowCells {
		excelColIndex := cIdx + 1

		// Skip the SQL reference column itself and empty cells
		if excelColIndex == sqlColNum || originalCellValue == "" {
			continue
		}
		// Quick check if it looks like a template/placeholder to process
		if !strings.Contains(originalCellValue, "{{") || !strings.Contains(originalCellValue, "}}") {
			continue
		}

		cellAxis, _ := excelize.CoordinatesToCellName(excelColIndex, excelRowIndex)
		cl := l.With(slog.String("cell", cellAxis))

		// Process the cell content using the fetched data
		processedValue, err := getCellValueFromTemplate(originalCellValue, dataMap)
		if err != nil {
			cl.Warn("Failed to process cell content template (leaving original value)",
				slog.String("error", err.Error()),
				slog.String("template_content", originalCellValue),
			)
			continue // Continue with next cell
		}

		// Encode maps/slices to JSON strings if necessary
		processedValue, err = encodeMapsAndSlices(processedValue)
		if err != nil {
			return fmt.Errorf("encode maps/slices for cell %s: %w", cellAxis, err)
		}

		// Compare string representations to avoid unnecessary SetCellValue calls
		if fmt.Sprint(processedValue) != originalCellValue {
			cl.Debug("Setting processed cell value", slog.Any("value", processedValue))
			if err := file.SetCellValue(sheetName, cellAxis, processedValue); err != nil {
				// Log warning, but maybe don't fail the whole report? Depends on requirements.
				cl.Warn("Failed to set processed cell value", slog.Any("error", err))
			}
		}
	}

	return nil
}

func encodeMapsAndSlices(v any) (any, error) {
	if v == nil {
		return nil, nil
	}
	rv := reflect.ValueOf(v)
	kind := rv.Kind()
	if kind == reflect.Ptr {
		if rv.IsNil() {
			return v, nil
		}
		rv = rv.Elem()
		kind = rv.Kind()
	}
	switch kind {
	case reflect.Map, reflect.Slice:
		jsonData, err := json.Marshal(v)
		if err != nil {
			return nil, fmt.Errorf("marshal to JSON: %w", err)
		}
		return string(jsonData), nil
	default:
		return v, nil
	}
}

// Regex for matching very simple template placeholders like {{ .key }}.
var templateRegex = regexp.MustCompile(`^\s*\{\{\s*\.\s*([a-zA-Z0-9_]+)\s*\}\}\s*$`)

const templateRegexExpectedMatches = 2 // 0 is the full match, 1 is the first capture group (the key)

func getCellValueFromTemplate(cellContent string, dataMap map[string]any) (any, error) {
	matches := templateRegex.FindStringSubmatch(cellContent)

	if len(matches) == templateRegexExpectedMatches {
		key := matches[1]
		if value, ok := dataMap[key]; ok {
			return value, nil
		}

		// Fallthrough
	}

	// Fallback to using the template engine for more complex replacements. Everything will be a string from here on.
	tmpl, err := template.New("contentTemplate").Parse(cellContent)
	if err != nil {
		return nil, fmt.Errorf("error parsing template: %w", err)
	}

	var buf bytes.Buffer
	err = tmpl.Execute(&buf, dataMap)
	if err != nil {
		return nil, fmt.Errorf("error executing template: %w", err)
	}

	return buf.String(), nil
}

func copyFile(src, dst string) error {
	sourceFileStat, err := os.Stat(src)
	if err != nil {
		if os.IsNotExist(err) {
			return fmt.Errorf("source file %q does not exist", src)
		}
		return fmt.Errorf("stat source file %q: %w", src, err)
	}
	if !sourceFileStat.Mode().IsRegular() {
		return fmt.Errorf("source %q is not a regular file", src)
	}
	source, err := os.Open(src)
	if err != nil {
		return fmt.Errorf("open source file %q: %w", src, err)
	}
	defer source.Close()
	dstDir := filepath.Dir(dst)
	if err = os.MkdirAll(dstDir, 0o750); err != nil {
		return fmt.Errorf("create destination directory %q: %w", dstDir, err)
	}
	destination, err := os.Create(dst)
	if err != nil {
		return fmt.Errorf("create destination file %q: %w", dst, err)
	}
	defer destination.Close()
	_, err = io.Copy(destination, source)
	if err != nil {
		return fmt.Errorf("copy content from %q to %q: %w", src, dst, err)
	}
	if err = destination.Sync(); err != nil {
		return fmt.Errorf("sync destination file %q: %w", dst, err)
	}
	return nil
}

// Helper for logging map keys (optional).
func mapsKeys(m map[string]any) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	return keys
}
