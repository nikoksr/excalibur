package datasource

import (
	"context"
)

type Config struct {
	DSN string // Data Source Name for the database connection.
}

func (c Config) Valid(_ context.Context) map[string]string {
	problems := make(map[string]string)

	if c.DSN == "" {
		problems["dsn"] = "must not be empty"
	}

	return problems
}
