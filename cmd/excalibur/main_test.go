package main

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"net"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"
	"github.com/xuri/excelize/v2"

	cliapp "excalibur/internal/cli"
)

const testdataDir = "testdata"

func TestExcaliburE2E_Success(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping E2E test in short mode")
	}
	t.Parallel()

	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Minute)
	defer cancel()

	// 1. Setup Test Database
	dbDSN, dbCleanup := setupTestDatabase(ctx, t)
	defer dbCleanup()

	// 2. Create Temporary Files and Directories
	var (
		testTemplateFileName = filepath.Join(testdataDir, "template.xlsx")
		testOutputFileName   = filepath.Join(testdataDir, "output.xlsx")
		testExpectedFileName = filepath.Join(testdataDir, "output_expected.xlsx")
	)

	tempBaseDir := t.TempDir()
	templatePath, expectedPath, outputPath := createTestFiles(
		t,
		tempBaseDir,
		testTemplateFileName,
		testExpectedFileName,
		filepath.Base(testOutputFileName), // Pass only the base name for output
	)
	// Queries dir relative to the *temporary* template path
	tempQueriesDir := filepath.Join(filepath.Dir(templatePath), "sql")

	// 3. Prepare Arguments for app.Run
	// Use the flag names defined in internal/cli/cli.go
	args := []string{
		"excalibur",
		"--dsn", dbDSN,
		"--report-template-path", templatePath,
		"--report-ref-col", "R",
		"--report-queries-dir", tempQueriesDir,
		"--report-output-path", outputPath,
		"--report-timeout", "1m",
		// "--verbose",
	}

	// 4. Create and Execute the CLI Application
	app := cliapp.NewApp("test-version", runExcalibur)
	runErr := app.Run(ctx, args)
	require.NoError(t, runErr, "app.Run failed unexpectedly")

	// 5. Verify Output
	_, err := os.Stat(outputPath)
	require.NoError(t, err, "Output file was not created at %s", outputPath)
	err = compareExcelFiles(t, templatePath, expectedPath, outputPath)
	require.NoError(t, err, "Generated Excel file does not match expected output value or template style")
}

func TestExcaliburE2E_MissingSQLFile(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping E2E test in short mode")
	}
	t.Parallel()

	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Minute)
	defer cancel()

	// 1. Setup Test Database
	dbDSN, dbCleanup := setupTestDatabase(ctx, t)
	defer dbCleanup()

	// 2. Create Temporary Files - Use the template with the invalid path reference
	testTemplateInvalidPathFileName := filepath.Join(
		testdataDir,
		"template_with_invalid_path.xlsx",
	)

	tempBaseDir := t.TempDir()
	dummyOutputBaseName := "output_for_failure_test.xlsx"
	templatePath, _, outputPath := createTestFiles(
		t,
		tempBaseDir,
		testTemplateInvalidPathFileName,
		"", // No expected file needed
		dummyOutputBaseName,
	)
	tempQueriesDir := filepath.Join(filepath.Dir(templatePath), "sql")

	// 3. Prepare Arguments
	args := []string{
		"excalibur",
		"--dsn", dbDSN,
		"--report-template-path", templatePath,
		"--report-ref-col", "R",
		"--report-queries-dir", tempQueriesDir,
		"--report-output-path", outputPath,
		"--report-timeout", "1m",
		// "--verbose",
	}

	// 4. Create and Execute the CLI Application
	app := cliapp.NewApp("test-version", runExcalibur)
	runErr := app.Run(ctx, args)
	require.Error(t, runErr, "app.Run should have failed due to missing SQL file")

	require.ErrorContains(t, runErr, "referenced SQL file not found", "Error message should indicate file not found")
	require.ErrorContains(t, runErr, "invalid_path.sql", "Error message should mention the missing file")
}

// --- Helper Functions ---

func createTestFiles(
	t *testing.T,
	tempBaseDir string,
	templateSourceBaseName, expectedSourceBaseName, outputBaseName string,
) (string, string, string) {
	t.Helper()

	relativeInputDir := "input"
	relativeOutputDir := "output"
	relativeSQLDirTarget := filepath.Join(relativeInputDir, "sql")

	inputDirAbs := filepath.Join(tempBaseDir, relativeInputDir)
	outputDirAbs := filepath.Join(tempBaseDir, relativeOutputDir)
	sqlDirTargetAbs := filepath.Join(tempBaseDir, relativeSQLDirTarget)

	require.NoError(t, os.MkdirAll(inputDirAbs, 0o755))
	require.NoError(t, os.MkdirAll(outputDirAbs, 0o755))
	require.NoError(t, os.MkdirAll(sqlDirTargetAbs, 0o755))

	cwd, err := os.Getwd()
	require.NoError(t, err)
	sourceTestDataDir := filepath.Join(cwd, testdataDir)
	sourceSQLDirAbs := filepath.Join(sourceTestDataDir, "queries")

	// --- Copy Template ---
	templateSourcePath := filepath.Join(sourceTestDataDir, filepath.Base(templateSourceBaseName))
	templatePath := filepath.Join(inputDirAbs, filepath.Base(templateSourceBaseName))
	_, err = os.Stat(templateSourcePath)
	require.NoError(t, err, "Ensure template file exists at %s", templateSourcePath)
	require.NoError(t, copyFileForTest(templateSourcePath, templatePath), "Failed to copy template file")

	// --- Copy Expected (Only if provided) ---
	var expectedPath string
	if expectedSourceBaseName != "" {
		expectedSourcePath := filepath.Join(sourceTestDataDir, filepath.Base(expectedSourceBaseName))
		expectedPath = filepath.Join(inputDirAbs, filepath.Base(expectedSourceBaseName))
		_, err = os.Stat(expectedSourcePath)
		require.NoError(t, err, "Ensure expected output file exists at %s", expectedSourcePath)
		require.NoError(t, copyFileForTest(expectedSourcePath, expectedPath), "Failed to copy expected output file")
	}

	// --- Define Output Path ---
	var outputPath string
	if outputBaseName != "" {
		outputPath = filepath.Join(outputDirAbs, filepath.Base(outputBaseName))
	}

	// --- Copy SQL Files ---
	sourceSQLDirStat, err := os.Stat(sourceSQLDirAbs)
	require.NoError(t, err)
	require.True(t, sourceSQLDirStat.IsDir())
	err = filepath.WalkDir(sourceSQLDirAbs, func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if !d.IsDir() && strings.HasSuffix(strings.ToLower(d.Name()), ".sql") {
			relPath, err := filepath.Rel(sourceSQLDirAbs, path) //nolint:govet // False positive err shadowing
			if err != nil {
				return fmt.Errorf("get relative path for %s: %w", path, err)
			}
			destPath := filepath.Join(sqlDirTargetAbs, relPath)
			destSubDir := filepath.Dir(destPath)
			if err := os.MkdirAll(destSubDir, 0o755); err != nil {
				return fmt.Errorf("create destination subdirectory %s: %w", destSubDir, err)
			}
			if err := copyFileForTest(path, destPath); err != nil {
				return fmt.Errorf("copy SQL file from %s to %s: %w", path, destPath, err)
			}
		}
		return nil
	})
	require.NoError(t, err, "Error walking/copying source SQL directory '%s'", sourceSQLDirAbs)

	return templatePath, expectedPath, outputPath
}

func copyFileForTest(src, dst string) error {
	data, err := os.ReadFile(src)
	if err != nil {
		return fmt.Errorf("read file %s: %w", src, err)
	}
	err = os.WriteFile(dst, data, 0o644)
	if err != nil {
		return fmt.Errorf("write file %s: %w", dst, err)
	}
	return nil
}

func setupTestDatabase(ctx context.Context, t *testing.T) (string, func()) {
	t.Helper()
	pgContainer, err := postgres.Run(ctx, "postgres:17-alpine",
		postgres.WithDatabase("testdb"),
		postgres.WithUsername("user"),
		postgres.WithPassword("password"),
		testcontainers.WithWaitStrategy(
			wait.ForLog("database system is ready to accept connections").
				WithOccurrence(2).
				WithStartupTimeout(5*time.Minute),
		),
	)
	require.NoError(t, err, "Failed to start PostgreSQL container")

	// Construct DSN
	host, err := pgContainer.Host(ctx)
	require.NoError(t, err)
	port, err := pgContainer.MappedPort(ctx, "5432/tcp")
	require.NoError(t, err)
	dsn := fmt.Sprintf("postgres://user:password@%s/testdb?sslmode=disable", net.JoinHostPort(host, port.Port()))

	// --- Apply schema from file ---
	cwd, err := os.Getwd() // Get CWD where test is run
	require.NoError(t, err)
	// Construct path relative to CWD
	schemaFilePath := filepath.Join(cwd, testdataDir, "schema.sql") // Adjusted path construction

	schemaBytes, err := os.ReadFile(schemaFilePath)
	require.NoError(t, err, "Failed to read schema file %s", schemaFilePath)
	schemaSQL := string(schemaBytes)
	require.NotEmpty(t, strings.TrimSpace(schemaSQL), "Schema file %s is empty", schemaFilePath)

	// Execute schema
	exitCode, _, err := pgContainer.Exec(ctx, []string{"psql", "-U", "user", "-d", "testdb", "-c", schemaSQL})
	require.NoError(t, err, "Failed to execute schema setup command in container")
	require.Equal(t, 0, exitCode, "Schema setup command failed with non-zero exit code")

	cleanup := func() {
		termCtx, termCancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer termCancel()
		if err := pgContainer.Terminate(termCtx); err != nil {
			t.Logf("Warning: failed to terminate test container: %v", err)
		}
	}

	return dsn, cleanup
}

func compareExcelFiles(t *testing.T, templatePath, expectedPath, actualPath string) error {
	t.Helper()

	// Open all three files
	templateFile, err := excelize.OpenFile(templatePath)
	if err != nil {
		return fmt.Errorf("open template file %q: %w", templatePath, err)
	}
	defer templateFile.Close() // Use defer inside the function

	expectedFile, err := excelize.OpenFile(expectedPath)
	if err != nil {
		return fmt.Errorf("open expected file %q: %w", expectedPath, err)
	}
	defer expectedFile.Close() // Use defer inside the function

	actualFile, err := excelize.OpenFile(actualPath)
	if err != nil {
		return fmt.Errorf("open actual file %q: %w", actualPath, err)
	}
	defer actualFile.Close() // Use defer inside the function

	expectedSheets := expectedFile.GetSheetList()
	actualSheets := actualFile.GetSheetList()
	templateSheets := templateFile.GetSheetList()

	// Basic sheet checks
	if !assert.ElementsMatch(t, expectedSheets, actualSheets, "Sheet names do not match between expected and actual") {
		return fmt.Errorf("sheet mismatch (expected vs actual): expected %v, got %v", expectedSheets, actualSheets)
	}
	if len(expectedSheets) > 0 &&
		!assert.Contains(
			t,
			templateSheets,
			expectedSheets[0],
			"Template file missing expected sheet %s",
			expectedSheets[0],
		) {
		t.Logf("Warning: Template sheets %v might differ from expected/actual %v", templateSheets, expectedSheets)
	}

	for _, sheetName := range expectedSheets {
		templateSheetExists := slices.Contains(templateSheets, sheetName)

		expectedRows, err := expectedFile.GetRows(sheetName)
		if err != nil {
			return fmt.Errorf("get rows from expected sheet %q: %w", sheetName, err)
		}
		actualRows, err := actualFile.GetRows(sheetName)
		if err != nil {
			return fmt.Errorf("get rows from actual sheet %q: %w", sheetName, err)
		}

		if !assert.Len(
			t,
			actualRows, len(expectedRows),
			"Number of rows in sheet '%s' does not match",
			sheetName,
		) {
			t.Logf(
				"Row count mismatch in sheet '%s': Expected %d, Got %d. Skipping detailed row comparison.",
				sheetName,
				len(expectedRows),
				len(actualRows),
			)
			continue
		}

		maxRows := len(expectedRows)
		for rIdx := range maxRows { // Correct loop condition
			expectedRow := expectedRows[rIdx]
			actualRow := actualRows[rIdx] // Safe due to length check above

			maxCols := max(len(expectedRow), len(actualRow)) // Use max for comparison loop

			for cIdx := range maxCols { // Correct loop condition
				cellName, err := excelize.CoordinatesToCellName(cIdx+1, rIdx+1)
				require.NoError(t, err, "Failed to convert coordinates to cell name for row %d, col %d", rIdx+1, cIdx+1)

				// Value Comparison
				expectedVal := ""
				if cIdx < len(expectedRow) {
					expectedVal = expectedRow[cIdx]
				}
				actualVal := ""
				if cIdx < len(actualRow) {
					actualVal = actualRow[cIdx]
				}
				if !assert.Equal(
					t,
					expectedVal,
					actualVal,
					"Value mismatch in sheet '%s', cell %s",
					sheetName,
					cellName,
				) {
					// Log difference but continue checking other cells/styles
					t.Logf("Value mismatch detail: Expected='%s', Got='%s'", expectedVal, actualVal)
				}

				// Style Comparison (Template vs Actual)
				if !templateSheetExists {
					t.Logf("Skipping style comparison for sheet '%s' as it's not in the template file.", sheetName)
					continue
				}

				templateStyleID, err := templateFile.GetCellStyle(sheetName, cellName)
				if err != nil { // Handle potential errors gracefully
					t.Logf(
						"Warning: Could not get template style for sheet '%s', cell %s: %v",
						sheetName,
						cellName,
						err,
					)
					continue // Skip style comparison for this cell
				}
				templateStyle, err := templateFile.GetStyle(templateStyleID)
				if err != nil {
					t.Logf("Warning: Could not get template style struct for ID %d: %v", templateStyleID, err)
					continue
				}

				actualStyleID, err := actualFile.GetCellStyle(sheetName, cellName)
				if err != nil {
					t.Logf(
						"Warning: Could not get actual style for sheet '%s', cell %s: %v",
						sheetName,
						cellName,
						err,
					)
					continue
				}
				actualStyle, err := actualFile.GetStyle(actualStyleID)
				if err != nil {
					t.Logf("Warning: Could not get actual style struct for ID %d: %v", actualStyleID, err)
					continue
				}

				// Use cmp.Diff with IgnoreFields for Border.Color
				ignoreBorderColor := cmpopts.IgnoreFields(excelize.Border{}, "Color")
				if diff := cmp.Diff(templateStyle, actualStyle, ignoreBorderColor); diff != "" {
					// Use Errorf to fail the test on style mismatch
					t.Errorf(
						"Style mismatch for sheet '%s', cell %s (-want template +got actual):\n%s",
						sheetName,
						cellName,
						diff,
					)
				}
			}
		}
	}

	if t.Failed() {
		return errors.New("one or more assertions failed during Excel comparison")
	}

	return nil
}
