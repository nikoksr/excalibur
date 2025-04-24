package logging

import (
	"io"
	"log/slog"

	"github.com/nikoksr/assert-go"
)

func NewLogger(w io.Writer, verbose bool) *slog.Logger {
	var handler slog.Handler = slog.NewTextHandler(w, &slog.HandlerOptions{
		AddSource: false,
		Level:     slog.LevelInfo,
	})

	if verbose {
		handler = slog.NewJSONHandler(w, &slog.HandlerOptions{
			AddSource: true,
			Level:     slog.LevelDebug,
		})
	}

	logger := slog.New(handler)
	slog.SetDefault(logger)

	assert.Assert(logger != nil, "logger must not be nil")
	return logger
}
