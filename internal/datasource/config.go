package datasource

import (
	"context"
	"strings"
)

type Config struct {
	DSN string
}

func (c Config) Valid(_ context.Context) map[string]string {
	problems := make(map[string]string)
	if strings.TrimSpace(c.DSN) == "" {
		problems["dsn"] = "must not be empty"
	}

	// TODO: ?; Validate DSN format

	return problems
}
