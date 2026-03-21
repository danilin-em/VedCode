package providers

import (
	"context"
	"errors"
	"log/slog"
	"strings"
	"time"
)

const (
	defaultTimeout = 120 * time.Second
	maxRetries     = 3
	baseRetryDelay = time.Second
)

// HTTPError represents an HTTP response error with a status code.
type HTTPError struct {
	StatusCode int
	Body       string
}

func (e *HTTPError) Error() string {
	return e.Body
}

// retryOnRateLimit retries the given function with exponential backoff on rate limit errors.
func retryOnRateLimit(logger *slog.Logger, fn func(ctx context.Context) error) error {
	var lastErr error
	for attempt := range maxRetries {
		ctx, cancel := context.WithTimeout(context.Background(), defaultTimeout)
		lastErr = fn(ctx)
		cancel()

		if lastErr == nil {
			return nil
		}

		if !isRetryableError(lastErr) {
			return lastErr
		}

		if attempt < maxRetries-1 {
			delay := baseRetryDelay * time.Duration(1<<attempt)
			logger.Debug("retrying after rate limit",
				"attempt", attempt+1,
				"delay", delay,
				"error", lastErr,
			)
			time.Sleep(delay)
		}
	}
	return lastErr
}

// isRetryableError checks if the error is a rate limit, resource exhaustion,
// or temporary unavailability error.
func isRetryableError(err error) bool {
	if err == nil {
		return false
	}

	// Check for typed HTTP errors first (from GenericHTTPProvider).
	var httpErr *HTTPError
	if errors.As(err, &httpErr) {
		switch httpErr.StatusCode {
		case 429, 502, 503, 504:
			return true
		}
		return false
	}

	// Fallback: string matching for SDK errors (e.g. Gemini genai SDK)
	// that don't expose typed status codes.
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "429") ||
		strings.Contains(msg, "503") ||
		strings.Contains(msg, "rate limit") ||
		strings.Contains(msg, "resource exhausted") ||
		strings.Contains(msg, "resource_exhausted") ||
		strings.Contains(msg, "quota") ||
		strings.Contains(msg, "unavailable")
}
