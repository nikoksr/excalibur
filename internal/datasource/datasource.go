package datasource

import (
	"context"
	"errors"
)

var (
	// ErrQueryReturnedNoRows indicates that a query expected to return exactly one row returned none.
	ErrQueryReturnedNoRows = errors.New("query returned no rows")
	// ErrQueryReturnedMultipleRows indicates that a query expected to return exactly one row returned more than one.
	ErrQueryReturnedMultipleRows = errors.New("query returned multiple rows")
	// ErrDataSourceClosed indicates an operation was attempted on a closed data source.
	ErrDataSourceClosed = errors.New("data source is closed")
)

type DataSource interface {
	FetchData(ctx context.Context, query string) (map[string]any, error)
	Close(ctx context.Context) error
}
