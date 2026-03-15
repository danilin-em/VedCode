package trace

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"sync"
)

// consoleHandler writes human-readable messages to an io.Writer at Info+ level.
// Output matches log.Println with no flags (just the message, no timestamp).
type consoleHandler struct {
	mu sync.Mutex
	w  io.Writer
}

func (h *consoleHandler) Enabled(_ context.Context, level slog.Level) bool {
	return level >= slog.LevelInfo
}

func (h *consoleHandler) Handle(_ context.Context, r slog.Record) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	_, err := fmt.Fprintln(h.w, r.Message)
	return err
}

func (h *consoleHandler) WithAttrs(_ []slog.Attr) slog.Handler { return h }
func (h *consoleHandler) WithGroup(_ string) slog.Handler      { return h }

// multiHandler dispatches log records to multiple slog.Handlers.
type multiHandler struct {
	handlers []slog.Handler
}

func (m *multiHandler) Enabled(ctx context.Context, level slog.Level) bool {
	for _, h := range m.handlers {
		if h.Enabled(ctx, level) {
			return true
		}
	}
	return false
}

func (m *multiHandler) Handle(ctx context.Context, r slog.Record) error {
	for _, h := range m.handlers {
		if h.Enabled(ctx, r.Level) {
			if err := h.Handle(ctx, r); err != nil {
				return err
			}
		}
	}
	return nil
}

func (m *multiHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	handlers := make([]slog.Handler, len(m.handlers))
	for i, h := range m.handlers {
		handlers[i] = h.WithAttrs(attrs)
	}
	return &multiHandler{handlers: handlers}
}

func (m *multiHandler) WithGroup(name string) slog.Handler {
	handlers := make([]slog.Handler, len(m.handlers))
	for i, h := range m.handlers {
		handlers[i] = h.WithGroup(name)
	}
	return &multiHandler{handlers: handlers}
}

// NewLogger creates a configured slog.Logger.
//
// console: if true, always writes plain-text messages to stderr at Info+ level.
// enabled: if true, writes JSON trace logs at Debug+ level.
// path: trace log file path (empty = stderr for trace).
//
// When both console and trace target stderr, console handler is skipped
// to avoid duplicate output.
func NewLogger(enabled bool, path string, console bool) (*slog.Logger, io.Closer, error) {
	var handlers []slog.Handler
	var closer io.Closer

	traceToStderr := enabled && path == ""

	// Console handler: plain text to stderr at Info level.
	// Skip if trace already goes to stderr (avoid duplicate output).
	if console && !traceToStderr {
		handlers = append(handlers, &consoleHandler{w: os.Stderr})
	}

	// Trace handler: JSON to file or stderr at Debug level.
	if enabled {
		var w io.Writer
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
		handlers = append(handlers, handler)
	}

	switch len(handlers) {
	case 0:
		return slog.New(slog.NewTextHandler(io.Discard, nil)), nil, nil
	case 1:
		return slog.New(handlers[0]), closer, nil
	default:
		return slog.New(&multiHandler{handlers: handlers}), closer, nil
	}
}
