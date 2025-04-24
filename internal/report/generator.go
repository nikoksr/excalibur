package report

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
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
	config     Config
	logger     *slog.Logger
}

func NewGenerator(source datasource.DataSource, cfg Config, logger *slog.Logger) *Generator {
	assert.Assert(source != nil, "DataSource must not be nil")
	assert.Assert(logger != nil, "Logger must not be nil")
	assert.Assert(filepath.IsAbs(cfg.TemplatePath), "template path must be absolute")
	assert.Assert(filepath.IsAbs(cfg.OutputPath), "output path must be absolute")
	assert.Assert(filepath.IsAbs(cfg.QueriesDir), "queries directory must be absolute")

	logger = logger.With(slog.String("component", "ReportGenerator"))

	return &Generator{
		dataSource: source,
		config:     cfg,
		logger:     logger,
	}
}

// GenerateReport orchestrates the report generation:
// 1. Copies the template to the output path.
// 2. Opens the copied file.
// 3. Processes each sheet, looking for SQL references in rows.
// 4. Fetches data and replaces placeholders.
// 5. Saves the modified file.
// Respects context for cancellation/timeouts.
func (g *Generator) GenerateReport(ctx context.Context) error {
	g.logger.Info(
		"Starting report generation process",
		slog.String("template", g.config.TemplatePath),
		slog.String("output", g.config.OutputPath),
		slog.String("queries_dir", g.config.QueriesDir),
		slog.String("ref_column", g.config.DataSourceRefColumn),
	)

	// 1. Copy Template File -> Output Path
	g.logger.Debug(
		"Copying template file",
		slog.String("from", g.config.TemplatePath),
		slog.String("to", g.config.OutputPath),
	)
	if err := copyFile(g.config.TemplatePath, g.config.OutputPath); err != nil {
		g.logger.Error(
			"Failed to copy template file",
			slog.String("from", g.config.TemplatePath),
			slog.String("to", g.config.OutputPath),
			slog.String("error", err.Error()),
		)
		return fmt.Errorf("copy template file from %q to %q: %w", g.config.TemplatePath, g.config.OutputPath, err)
	}
	g.logger.Debug("Template file copied successfully")

	// 2. Open the copied file for modification
	g.logger.Debug("Opening copied report file for editing", slog.String("path", g.config.OutputPath))
	f, err := excelize.OpenFile(g.config.OutputPath)
	if err != nil {
		g.logger.Error(
			"Failed to open copied report file",
			slog.String("path", g.config.OutputPath),
			slog.String("error", err.Error()),
		)
		return fmt.Errorf("open copied report file %q: %w", g.config.OutputPath, err)
	}
	defer func() {
		g.logger.Debug("Attempting to close report file", slog.String("path", g.config.OutputPath))
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
		err = fmt.Errorf("template file %q contains no sheets", g.config.TemplatePath)
		g.logger.Error(err.Error())
		return err
	}
	g.logger.Debug("Found sheets in template", slog.Any("sheet_names", sheetList))

	// Get the 0-based index for the SQL reference column (e.g., "R" -> 17)
	sqlColNum, err := excelize.ColumnNameToNumber(g.config.DataSourceRefColumn)
	if err != nil {
		g.logger.Error(
			"Internal error: invalid DataSourceRefColumn",
			slog.String("column_name", g.config.DataSourceRefColumn),
			slog.String("error", err.Error()),
		)
		return fmt.Errorf("internal error: invalid DataSourceRefCol %q: %w", g.config.DataSourceRefColumn, err)
	}
	zeroBasedSQLColIndex := sqlColNum - 1
	g.logger.Debug(
		"Determined SQL reference column index",
		slog.String("column_name", g.config.DataSourceRefColumn),
		slog.Int("0_based_index", zeroBasedSQLColIndex),
	)

	// 4. Process Sheets and Rows
	g.logger.Info("Starting sheet processing...")
	for i, sheetName := range sheetList {
		sheetLogger := g.logger.With(slog.String("sheet_name", sheetName), slog.Int("sheet_index", i))
		sheetLogger.Info("Processing sheet")

		// Process the current sheet, checking context periodically.
		if err := g.processSheet(ctx, f, sheetName, zeroBasedSQLColIndex, g.config.QueriesDir, sheetLogger); err != nil {
			return fmt.Errorf("processing sheet %q: %w", sheetName, err)
		}

		// Check for context cancellation after each sheet for faster interruption.
		if err := ctx.Err(); err != nil {
			errMsg := fmt.Sprintf("processing interrupted after sheet %q", sheetName)
			g.logger.Warn(errMsg, slog.String("reason", err.Error()))
			return fmt.Errorf("%s: %w", errMsg, err)
		}
		sheetLogger.Info("Finished processing sheet")
	}
	g.logger.Info("Finished processing all sheets.")

	// 5. Save the final report
	// Update formulas/links before saving, crucial if formulas depend on generated data.
	g.logger.Debug("Updating linked values and formulas in the workbook...")
	if err := f.UpdateLinkedValue(); err != nil {
		g.logger.Warn(
			"Failed to update linked values/formulas; results may be inconsistent",
			slog.String("error", err.Error()),
		)
	}

	g.logger.Info("Saving generated report...", slog.String("path", g.config.OutputPath))
	if err := f.Save(); err != nil {
		g.logger.Error(
			"Failed to save the generated report file",
			slog.String("path", g.config.OutputPath),
			slog.String("error", err.Error()),
		)
		return fmt.Errorf("save generated report file %q: %w", g.config.OutputPath, err)
	}

	return nil
}

// processSheet iterates through rows of a single sheet and triggers row processing.
// Uses GetRows which reads the whole sheet; consider Stream Reader for very large files.
func (g *Generator) processSheet(
	ctx context.Context,
	file *excelize.File,
	sheetName string,
	zeroBasedSQLColIndex int,
	queryBaseDir string,
	logger *slog.Logger,
) error {
	rows, err := file.GetRows(sheetName)
	if err != nil {
		logger.Error("Failed to get rows from sheet", slog.String("error", err.Error()))
		return fmt.Errorf("get rows from sheet %q: %w", sheetName, err)
	}

	logger.Debug("Sheet contains rows", slog.Int("row_count", len(rows)))
	if len(rows) == 0 {
		logger.Info("Sheet is empty, skipping.")
		return nil
	}

	// Process each row
	for rowIndex, rowCells := range rows {
		excelRowIndex := rowIndex + 1 // Excel rows are 1-based
		rowLogger := logger.With(slog.Int("row_index_excel", excelRowIndex))

		if err := ctx.Err(); err != nil {
			errMsg := fmt.Sprintf("processing interrupted on sheet %q before row %d", sheetName, excelRowIndex)
			rowLogger.Warn(errMsg, slog.String("reason", err.Error()))
			return fmt.Errorf("%s: %w", errMsg, err) // Return context error
		}

		if err := g.processRow(ctx, file, sheetName, excelRowIndex, rowCells, zeroBasedSQLColIndex, queryBaseDir, rowLogger); err != nil {
			return fmt.Errorf("processing row %d: %w", excelRowIndex, err)
		}
	}
	return nil
}

// processRow handles the logic for a single row: finds SQL ref, fetches data, replaces placeholders.
func (g *Generator) processRow(
	ctx context.Context,
	file *excelize.File,
	sheetName string,
	excelRowIndex int,
	rowCells []string,
	zeroBasedSQLColIndex int,
	queryBaseDir string,
	logger *slog.Logger,
) error {
	// --- 1. Check for SQL Reference ---
	if len(rowCells) <= zeroBasedSQLColIndex {
		return nil // Row too short for ref column
	}
	sqlFilePathRelative := strings.TrimSpace(rowCells[zeroBasedSQLColIndex])
	if sqlFilePathRelative == "" {
		return nil // No SQL reference in this row
	}

	// Construct and clean the absolute path to the SQL file.
	sqlFilePathAbsolute := filepath.Join(queryBaseDir, sqlFilePathRelative)
	sqlFilePathAbsolute = filepath.Clean(sqlFilePathAbsolute) // Basic path sanitization

	logger = logger.With(
		slog.String("sql_file_relative", sqlFilePathRelative),
		slog.String("sql_file_absolute", sqlFilePathAbsolute),
	)
	logger.Info("Found SQL reference, processing row")

	// --- 2. Clear the SQL Reference Cell ---
	sqlCellAxis, err := excelize.CoordinatesToCellName(zeroBasedSQLColIndex+1, excelRowIndex)
	if err != nil {
		logger.Error(
			"Internal error: Failed to calculate SQL reference cell coordinates",
			slog.String("error", err.Error()),
		)
	} else {
		logger.Debug("Clearing SQL reference cell", slog.String("cell", sqlCellAxis))
		if err = file.SetCellValue(sheetName, sqlCellAxis, nil); err != nil {
			logger.Warn("Failed to clear SQL reference cell (continuing processing)", slog.String("cell", sqlCellAxis), slog.String("error", err.Error()))
		}
	}

	// --- 3. Read SQL Query File ---
	logger.Debug("Reading SQL query file")
	queryBytes, err := os.ReadFile(sqlFilePathAbsolute)
	if err != nil {
		if os.IsNotExist(err) {
			logger.Error("Referenced SQL file not found", slog.String("error", err.Error()))
			return fmt.Errorf("referenced SQL file not found at %q", sqlFilePathAbsolute)
		}
		logger.Error("Failed to read SQL file", slog.String("error", err.Error()))
		return fmt.Errorf("read SQL file %q: %w", sqlFilePathAbsolute, err)
	}

	query := string(queryBytes)
	trimmedQuery := strings.TrimSpace(query)
	if trimmedQuery == "" {
		logger.Warn("Skipping data fetch and replacement: SQL file is empty or contains only whitespace.")
		return nil
	}
	logger.Debug("SQL query read successfully", slog.String("query", trimmedQuery))

	// --- 4. Fetch Data ---
	logger.Debug("Fetching data from data source")
	dataMap, err := g.dataSource.FetchData(ctx, trimmedQuery)
	if err != nil {
		if errors.Is(err, datasource.ErrQueryReturnedNoRows) {
			logger.Warn("SQL query returned no rows, skipping replacements for this row.")
			return nil
		}

		logger.Error(
			"Failed to fetch data from data source, skipping row processing.",
			slog.String("error", err.Error()),
		)
		return fmt.Errorf("fetch data using query from %q: %w", sqlFilePathAbsolute, err)
	}

	if len(dataMap) == 0 {
		logger.Warn("Skipping marker replacement: Fetched data map is empty.")
		return nil
	}
	logger.Debug("Data fetched successfully", slog.Any("data_keys", getMapKeys(dataMap)))

	// --- 5. Replace Placeholders in Cells ---
	logger.Debug("Scanning row cells for placeholders...")
	for cellIndex, originalCellValue := range rowCells {
		// Skip the SQL ref column itself and cells without template markers.
		if cellIndex == zeroBasedSQLColIndex || !strings.Contains(originalCellValue, "{{") {
			continue
		}

		excelColIndex := cellIndex + 1
		cellAxis, _ := excelize.CoordinatesToCellName(excelColIndex, excelRowIndex)
		cellLogger := logger.With(slog.String("cell", cellAxis), slog.String("template_content", originalCellValue))
		cellLogger.Debug("Found potential template, processing cell content")

		// Process the cell content using the fetched data.
		processedValue, err := processTemplate(originalCellValue, dataMap)
		if err != nil {
			cellLogger.Warn(
				"Failed to process cell content template (leaving original value)",
				slog.String("error", err.Error()),
			)
			continue
		}

		// Encode maps/slices/pointers to JSON strings for Excel compatibility.
		finalValue, err := encodeComplexTypes(processedValue)
		if err != nil {
			cellLogger.Error(
				"Failed to encode complex data type for cell",
				slog.Any("value", processedValue),
				slog.String("error", err.Error()),
			)
			continue // Continue processing other cells
		}

		// Optimization: Only update cell if the value actually changed.
		if fmt.Sprint(finalValue) != originalCellValue {
			cellLogger.Debug("Setting processed cell value", slog.Any("new_value", finalValue))
			if err := file.SetCellValue(sheetName, cellAxis, finalValue); err != nil {
				cellLogger.Warn(
					"Failed to set processed cell value",
					slog.Any("value", finalValue),
					slog.String("error", err.Error()),
				)
			}
		} else {
			cellLogger.Debug("Skipping cell update: Processed value is same as original.")
		}
	}

	logger.Info("Finished processing row")
	return nil
}

// encodeComplexTypes checks if a value is a map, slice, or pointer to one,
// and marshals it to a JSON string if so. Otherwise, returns the value unchanged.
// This helps embed complex data structures into Excel cells legibly.
func encodeComplexTypes(v any) (any, error) {
	if v == nil {
		return nil, nil
	}

	rv := reflect.ValueOf(v)

	// Dereference pointer if not nil
	if rv.Kind() == reflect.Ptr {
		if rv.IsNil() {
			return nil, nil
		}
		rv = rv.Elem()
	}

	switch rv.Kind() {
	case reflect.Map, reflect.Slice:
		if rv.Kind() == reflect.Slice && rv.Type().Elem().Kind() == reflect.Uint8 {
			return string(rv.Bytes()), nil
		}

		jsonData, err := json.Marshal(v)
		if err != nil {
			return nil, fmt.Errorf("marshal complex type (%T) to JSON: %w", v, err)
		}

		return string(jsonData), nil
	default:
		return v, nil // For other types, return as-is.
	}
}

// Simple template placeholder regex: {{ .key }} or {{.key}} (with optional whitespace).
var simpleTemplateRegex = regexp.MustCompile(`^\s*\{\{\s*\.\s*([a-zA-Z0-9_]+)\s*\}\}\s*$`)

const simpleTemplateRegexKeyIndex = 1 // Index of the capture group for the key name.

// processTemplate evaluates a cell's content using the provided data map. It uses a fast path for simple `{{ .key }}`
// placeholders and falls back to the full `text/template` engine for more complex expressions.
func processTemplate(cellContent string, dataMap map[string]any) (any, error) {
	// Fast path: Check if the entire cell content matches the simple `{{ .key }}` pattern.
	matches := simpleTemplateRegex.FindStringSubmatch(cellContent)
	if len(matches) == simpleTemplateRegexKeyIndex+1 {
		key := matches[simpleTemplateRegexKeyIndex]
		if value, ok := dataMap[key]; ok {
			return value, nil // Key found
		}

		// If key not found, fall through to text/template
	}

	// Fallback: Use text/template for complex templates or if simple match failed/key missing.
	// Note: text/template always produces a string output.
	tmpl, err := template.New("cell").
		Option("missingkey=error"). // Missing key will return an error instead of ignoring it.
		Parse(cellContent)
	if err != nil {
		return nil, fmt.Errorf("parse cell template: %w", err)
	}

	var buf bytes.Buffer
	if err = tmpl.Execute(&buf, dataMap); err != nil {
		return nil, fmt.Errorf("execute cell template: %w", err)
	}

	return buf.String(), nil
}

func copyFile(src, dst string) error {
	sourceFileStat, err := os.Stat(src)
	if err != nil {
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

	// Create dest file, truncating if exists, using source permissions.
	destination, err := os.OpenFile(dst, os.O_RDWR|os.O_CREATE|os.O_TRUNC, sourceFileStat.Mode())
	if err != nil {
		return fmt.Errorf("create destination file %q: %w", dst, err)
	}
	defer destination.Close()

	_, err = io.Copy(destination, source)
	if err != nil {
		return fmt.Errorf("copy content from %q to %q: %w", src, dst, err)
	}

	// Ensure the destination file is synced to disk.
	if err = destination.Sync(); err != nil {
		return fmt.Errorf("sync destination file %q: %w", dst, err)
	}

	return nil
}

func getMapKeys(m map[string]any) []string {
	if m == nil {
		return nil
	}

	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}

	return keys
}
