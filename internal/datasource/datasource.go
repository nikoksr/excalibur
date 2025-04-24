package datasource

import (
	"context"
	"errors"
)

var (
	// ErrQueryReturnedNoRows indicates a query expected exactly one row but found none.
	ErrQueryReturnedNoRows = errors.New("query returned no rows")

	// ErrQueryReturnedMultipleRows indicates a query expected exactly one row but found more. Often wraps
	// pgx.ErrTooManyRows or similar driver errors.
	ErrQueryReturnedMultipleRows = errors.New("query returned multiple rows")

	// ErrDataSourceClosed indicates an operation was attempted on a closed data source.
	ErrDataSourceClosed = errors.New("data source is closed")
)

type DataSource interface {
	FetchData(ctx context.Context, query string) (map[string]any, error)
	Close(ctx context.Context) error
}
