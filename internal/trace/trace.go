package trace

import (
	"fmt"
	"io"
	"log/slog"
	"os"
)

// NewLogger creates a configured slog.Logger for trace logging.
// If enabled is false, returns a no-op logger.
// If path is empty and enabled is true, writes JSON to stderr.
// If path is set, writes JSON to that file.
// Returns an io.Closer that should be deferred if non-nil.
func NewLogger(enabled bool, path string) (*slog.Logger, io.Closer, error) {
	if !enabled {
		return slog.New(slog.NewTextHandler(io.Discard, nil)), nil, nil
	}

	var w io.Writer
	var closer io.Closer

	if path != "" {
		f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
		if err != nil {
			return nil, nil, fmt.Errorf("opening trace log file: %w", err)
		}
		w = f
		closer = f
	} else {
		w = os.Stderr
	}

	handler := slog.NewJSONHandler(w, &slog.HandlerOptions{
		Level:     slog.LevelDebug,
		AddSource: true,
	})

	return slog.New(handler), closer, nil
}
