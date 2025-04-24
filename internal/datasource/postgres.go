package datasource

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync/atomic"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/nikoksr/assert-go"
)

var _ DataSource = (*PostgresDataSource)(nil)

type PostgresDataSource struct {
	pool   *pgxpool.Pool
	closed atomic.Bool
	logger *slog.Logger
}

func NewPostgresDataSource(ctx context.Context, cfg Config) (*PostgresDataSource, error) {
	assert.Assert(ctx != nil, "context must not be nil")
	assert.Assert(cfg.DSN != "", "DSN must not be empty")

	logger := slog.Default().With(slog.String("component", "PostgresDataSource"))

	config, err := pgxpool.ParseConfig(cfg.DSN)
	if err != nil {
		return nil, fmt.Errorf("parse database config from DSN: %w", err)
	}

	logger.Debug("Parsed database config",
		slog.String("host", config.ConnConfig.Host),
		slog.Int("port", int(config.ConnConfig.Port)),
		slog.String("user", config.ConnConfig.User),
	)

	pool, err := pgxpool.NewWithConfig(ctx, config)
	if err != nil {
		return nil, fmt.Errorf("create database connection pool: %w", err)
	}

	logger.Info("Pinging database pool...")
	if err := pool.Ping(ctx); err != nil {
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
	assert.Assert(p.pool != nil, "Database connection pool is nil")

	if query == "" {
		return nil, errors.New("query must not be empty")
	}

	if p.closed.Load() {
		return nil, ErrDataSourceClosed
	}

	rows, err := p.pool.Query(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("execute query: %w", err)
	}

	resultMap, err := pgx.CollectOneRow(rows, pgx.RowToMap)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrQueryReturnedNoRows
		}
		return nil, fmt.Errorf("collect row: %w", err)
	}

	// Post processing the result map to handle specific types
	for key, value := range resultMap {
		switch v := value.(type) {
		case pgtype.Numeric:
			floatVal, err := v.Float64Value()
			assert.Assert(err == nil, "Failed to convert pgtype.Numeric to float64")
			assert.Assert(floatVal.Valid, "Expected pgtype.Numeric to be valid")

			// Replace pgtype.Numeric with its float64 representation, pgtype.Numeric is a struct and gets printed as
			// such.
			resultMap[key] = floatVal.Float64

		default:
		}
	}

	return resultMap, nil
}

func (p *PostgresDataSource) Close(ctx context.Context) error {
	assert.Assert(ctx != nil, "context must not be nil")

	if !p.closed.CompareAndSwap(false, true) {
		return nil
	}

	p.logger.Info("Closing database connection pool...")
	p.pool.Close()

	return nil
}
