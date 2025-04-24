package datasource

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync/atomic"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/nikoksr/assert-go"
)

// Compile-time check to ensure PostgresDataSource implements the DataSource interface.
var _ DataSource = (*PostgresDataSource)(nil)

type PostgresDataSource struct {
	pool   *pgxpool.Pool
	closed atomic.Bool
	logger *slog.Logger
}

func NewPostgresDataSource(ctx context.Context, cfg Config, logger *slog.Logger) (*PostgresDataSource, error) {
	assert.Assert(ctx != nil, "context must not be nil")
	assert.Assert(cfg.DSN != "", "DSN must not be empty")
	assert.Assert(logger != nil, "logger must not be nil")

	logger = logger.With(slog.String("component", "PostgresDataSource"))

	logger.Info("Initializing PostgreSQL data source...")
	logger.Debug("Parsing database config from DSN")
	config, err := pgxpool.ParseConfig(cfg.DSN)
	if err != nil {
		logger.Error("Failed to parse DSN", slog.String("error", err.Error()))
		return nil, fmt.Errorf("parse database config from DSN: %w", err)
	}

	logger.Debug("Parsed database config",
		slog.String("host", config.ConnConfig.Host),
		slog.Int("port", int(config.ConnConfig.Port)),
		slog.String("user", config.ConnConfig.User),
		slog.String("database", config.ConnConfig.Database),
	)

	logger.Debug("Creating database connection pool...")
	pool, err := pgxpool.NewWithConfig(ctx, config)
	if err != nil {
		logger.Error("Failed to create database connection pool", slog.String("error", err.Error()))
		return nil, fmt.Errorf("create database connection pool: %w", err)
	}

	logger.Info("Pinging database pool...")
	if err := pool.Ping(ctx); err != nil {
		logger.Error("Failed to ping database", slog.String("error", err.Error()))
		pool.Close() // Attempt cleanup
		return nil, fmt.Errorf("ping database: %w", err)
	}

	logger.Info("Database connection pool established successfully.")

	return &PostgresDataSource{
		pool:   pool,
		logger: logger,
	}, nil
}

func (p *PostgresDataSource) FetchData(ctx context.Context, query string) (map[string]any, error) {
	assert.Assert(ctx != nil, "context must not be nil")
	assert.Assert(p.pool != nil, "database connection pool is nil")

	if p.closed.Load() {
		p.logger.Warn("Attempted to fetch data on a closed data source")
		return nil, ErrDataSourceClosed
	}

	trimmedQuery := strings.TrimSpace(query)
	if trimmedQuery == "" {
		return nil, errors.New("query must not be empty")
	}

	// --- SECURITY WARNING ---
	// Executing raw SQL strings (especially from external files) can be risky.
	// Ensure the source of SQL files is trusted or implement parameterization/sanitization
	// if queries could be influenced by untrusted input.
	p.logger.Debug("Executing query", slog.String("sql", trimmedQuery))

	rows, err := p.pool.Query(ctx, trimmedQuery)
	if err != nil {
		p.logger.Error("Failed to execute query", slog.String("sql", trimmedQuery), slog.String("error", err.Error()))
		return nil, fmt.Errorf("execute query: %w", err)
	}

	resultMap, err := pgx.CollectOneRow(rows, pgx.RowToMap)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			p.logger.Warn("Query returned no rows", slog.String("sql", trimmedQuery))
			return nil, ErrQueryReturnedNoRows
		}
		if errors.Is(err, pgx.ErrTooManyRows) {
			p.logger.Warn("Query returned multiple rows, expected one", slog.String("sql", trimmedQuery))
			return nil, fmt.Errorf("%w: %w", ErrQueryReturnedMultipleRows, err)
		}

		p.logger.Error(
			"Failed to collect row data",
			slog.String("sql", trimmedQuery),
			slog.String("error", err.Error()),
		)
		return nil, fmt.Errorf("collect single row: %w", err)
	}

	p.logger.Debug("Query returned one row successfully", slog.String("sql", trimmedQuery))

	// Post-process the map to convert specific pgx types into more standard Go types for easier template consumption.
	processedMap := make(map[string]any, len(resultMap))
	for key, value := range resultMap {
		processedMap[key] = p.convertPgValue(key, value)
	}

	return processedMap, nil
}

func (p *PostgresDataSource) convertPgValue(key string, value any) any {
	logger := p.logger.With(slog.String("key", key))

	switch v := value.(type) {
	case pgtype.Numeric:
		// Attempt to convert pgtype.Numeric to float64. This might lose precision for very large numbers.
		floatVal, err := v.Float64Value()
		if err != nil || !floatVal.Valid {
			logger.Warn("Failed to convert pgtype.Numeric to valid float64", slog.Any("original_value", v))
			return nil
		}

		logger.Debug("Converting pgtype.Numeric to float64", slog.Float64("value", floatVal.Float64))
		return floatVal.Float64

	case pgtype.Timestamptz:
		return convertPGTime(
			v,
			v.Time, v.InfinityModifier, v.Valid,
			"pgtype.Timestamptz",
			logger,
		)
	case pgtype.Timestamp:
		return convertPGTime(
			v,
			v.Time, v.InfinityModifier, v.Valid,
			"pgtype.Timestamp",
			logger,
		)
	case pgtype.Date:
		return convertPGTime(
			v,
			v.Time, v.InfinityModifier, v.Valid,
			"pgtype.Date",
			logger,
		)

	// TODO: ?; JSONB -> map[string]any or string, arrays -> slices

	default:
		return value // Return other types as-is.
	}
}

func convertPGTime(
	originalValue any,
	timeValue time.Time, infModifier pgtype.InfinityModifier, valid bool,
	pgTypeName string,
	logger *slog.Logger,
) any {
	if !valid {
		logger.Warn(fmt.Sprintf("%s value is invalid", pgTypeName), slog.Any("original_value", originalValue))
		return nil
	}

	logger.Debug(fmt.Sprintf("Converting %s to time.Time", pgTypeName), slog.Any("original_value", originalValue))

	switch infModifier {
	case pgtype.Infinity:
		return "infinity"
	case pgtype.NegativeInfinity:
		return "-infinity"
	case pgtype.Finite:
		fallthrough
	default:
		return timeValue
	}
}

func (p *PostgresDataSource) Close(ctx context.Context) error {
	assert.Assert(ctx != nil, "context must not be nil")
	assert.Assert(p.pool != nil, "database connection pool is nil")

	if !p.closed.CompareAndSwap(false, true) {
		p.logger.Debug("Close called on already closed data source.")
		return nil
	}

	p.pool.Close()
	p.logger.Info("Database connection pool closed.")

	return nil
}
