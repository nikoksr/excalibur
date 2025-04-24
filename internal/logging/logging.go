package logging

import (
	"io"
	"log/slog"

	"github.com/nikoksr/assert-go"
)

func NewLogger(w io.Writer, verbose bool) *slog.Logger {
	logLevel := slog.LevelInfo
	addSource := false
	if verbose {
		logLevel = slog.LevelDebug
		addSource = true
	}

	logger := slog.New(slog.NewTextHandler(w, &slog.HandlerOptions{
		Level:     logLevel,
		AddSource: addSource,
	}))
	slog.SetDefault(logger)

	assert.Assert(logger != nil, "logger must not be nil")
	return logger
}
