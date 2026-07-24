package middleware

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"log/slog"
	"net/http"
	"os"
	"strings"
	"time"
)

type contextKey string

const (
	requestIDKey contextKey = "request_id"
	traceIDKey   contextKey = "trace_id"
	loggerKey    contextKey = "logger"
)

// InitLogger configures slog's default logger to emit JSON using the shared
// Trident log schema and returns it. Every line carries the given service name;
// the standard keys are renamed so both the Go and Rust stacks emit identical
// field names: service, level, timestamp, message (+ structured fields such as
// request_id and trace_id). Respects LOG_LEVEL (debug|info|warn|error).
//
// See docs/observability/logging.md for the full schema.
func InitLogger(service string) *slog.Logger {
	handler := slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{
		Level: parseLevel(os.Getenv("LOG_LEVEL")),
		ReplaceAttr: func(_ []string, a slog.Attr) slog.Attr {
			switch a.Key {
			case slog.TimeKey:
				a.Key = "timestamp"
			case slog.MessageKey:
				a.Key = "message"
			}
			return a
		},
	})
	logger := slog.New(handler).With("service", service)
	slog.SetDefault(logger)
	return logger
}

func parseLevel(s string) slog.Level {
	switch strings.ToLower(s) {
	case "debug":
		return slog.LevelDebug
	case "warn", "warning":
		return slog.LevelWarn
	case "error":
		return slog.LevelError
	default:
		return slog.LevelInfo
	}
}

// RequestIDFromContext returns the request ID attached by the RequestID middleware.
func RequestIDFromContext(ctx context.Context) string {
	id, _ := ctx.Value(requestIDKey).(string)
	return id
}

// TraceIDFromContext returns the trace ID attached by the RequestID middleware,
// derived from an incoming W3C `traceparent` header when present.
func TraceIDFromContext(ctx context.Context) string {
	id, _ := ctx.Value(traceIDKey).(string)
	return id
}

// Logger returns the request-scoped logger (carrying request_id and trace_id)
// attached by the RequestID middleware, falling back to the default logger.
func Logger(ctx context.Context) *slog.Logger {
	if l, ok := ctx.Value(loggerKey).(*slog.Logger); ok {
		return l
	}
	return slog.Default()
}

// RequestID attaches correlation identifiers to the request context so every
// log line emitted while handling the request can be tied back to it (and to a
// distributed trace, once tracing is wired up in #290).
//
//   - request_id: reuses an inbound X-Request-ID header, or generates one.
//   - trace_id:   extracted from the W3C `traceparent` header if present.
//
// It also stores a request-scoped *slog.Logger pre-populated with both ids and
// echoes the request id back via the X-Request-ID response header.
func RequestID(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id := r.Header.Get("X-Request-ID")
		if id == "" {
			b := make([]byte, 8)
			_, _ = rand.Read(b)
			id = hex.EncodeToString(b)
		}
		traceID := traceIDFromTraceparent(r.Header.Get("traceparent"))

		ctx := context.WithValue(r.Context(), requestIDKey, id)
		ctx = context.WithValue(ctx, traceIDKey, traceID)
		ctx = context.WithValue(ctx, loggerKey,
			slog.Default().With("request_id", id, "trace_id", traceID))

		w.Header().Set("X-Request-ID", id)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// traceIDFromTraceparent parses the 32-hex-char trace-id out of a W3C
// `traceparent` header (`version-traceid-parentid-flags`). Returns "" if the
// header is absent or malformed.
func traceIDFromTraceparent(h string) string {
	if h == "" {
		return ""
	}
	parts := strings.Split(h, "-")
	if len(parts) < 4 {
		return ""
	}
	traceID := parts[1]
	if len(traceID) != 32 || !isHex(traceID) {
		return ""
	}
	return traceID
}

func isHex(s string) bool {
	_, err := hex.DecodeString(s)
	return err == nil
}

// statusRecorder wraps ResponseWriter to capture the HTTP status code.
type statusRecorder struct {
	http.ResponseWriter
	code int
}

func (sr *statusRecorder) WriteHeader(code int) {
	sr.code = code
	sr.ResponseWriter.WriteHeader(code)
}

// Logging emits a structured JSON log line after each request using the
// request-scoped logger, so the line carries service, request_id and trace_id.
func Logging(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		rec := &statusRecorder{ResponseWriter: w, code: http.StatusOK}

		next.ServeHTTP(rec, r)

		Logger(r.Context()).Info("request",
			"method", r.Method,
			"path", r.URL.Path,
			"status", rec.code,
			"latency_ms", time.Since(start).Milliseconds(),
		)
	})
}
