package providers

import (
	"io"
	"log/slog"
)

var noopLogger = slog.New(slog.NewTextHandler(io.Discard, nil))
