package middleware

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// newBufferLogger builds a JSON logger with the shared Trident schema that
// writes to buf and installs it as the default, mirroring InitLogger.
func newBufferLogger(buf *bytes.Buffer, service string) {
	handler := slog.NewJSONHandler(buf, &slog.HandlerOptions{
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
	slog.SetDefault(slog.New(handler).With("service", service))
}

func TestLoggingIncludesCorrelationIDs(t *testing.T) {
	var buf bytes.Buffer
	newBufferLogger(&buf, "trident-api")

	final := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// A handler logging via the request-scoped logger also gets the ids.
		Logger(r.Context()).Info("handler ran")
		w.WriteHeader(http.StatusTeapot)
	})

	chain := RequestID(Logging(final))

	req := httptest.NewRequest(http.MethodGet, "/v1/events", nil)
	req.Header.Set("traceparent", "00-0af7651916cd43dd8448eb211c80319c-b7ad6b7169203331-01")
	rec := httptest.NewRecorder()

	chain.ServeHTTP(rec, req)

	// Response echoes the generated request id.
	gotHeader := rec.Header().Get("X-Request-ID")
	if gotHeader == "" {
		t.Fatal("expected X-Request-ID response header to be set")
	}

	lines := strings.Split(strings.TrimSpace(buf.String()), "\n")
	if len(lines) < 2 {
		t.Fatalf("expected at least 2 log lines (handler + request), got %d: %q", len(lines), buf.String())
	}

	for _, line := range lines {
		var entry map[string]any
		if err := json.Unmarshal([]byte(line), &entry); err != nil {
			t.Fatalf("log line is not valid JSON: %v (%q)", err, line)
		}
		if entry["service"] != "trident-api" {
			t.Errorf("service = %v, want trident-api", entry["service"])
		}
		if _, ok := entry["timestamp"]; !ok {
			t.Errorf("log line missing timestamp: %q", line)
		}
		if entry["request_id"] != gotHeader {
			t.Errorf("request_id = %v, want %v", entry["request_id"], gotHeader)
		}
		if entry["trace_id"] != "0af7651916cd43dd8448eb211c80319c" {
			t.Errorf("trace_id = %v, want the traceparent trace-id", entry["trace_id"])
		}
	}
}

func TestReusesInboundRequestID(t *testing.T) {
	var buf bytes.Buffer
	newBufferLogger(&buf, "trident-api")

	final := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := RequestIDFromContext(r.Context()); got != "abc123" {
			t.Errorf("request id = %q, want abc123", got)
		}
	})

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("X-Request-ID", "abc123")
	rec := httptest.NewRecorder()

	RequestID(final).ServeHTTP(rec, req)

	if rec.Header().Get("X-Request-ID") != "abc123" {
		t.Errorf("X-Request-ID header = %q, want abc123", rec.Header().Get("X-Request-ID"))
	}
}

func TestTraceIDFromTraceparent(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{"valid", "00-0af7651916cd43dd8448eb211c80319c-b7ad6b7169203331-01", "0af7651916cd43dd8448eb211c80319c"},
		{"empty", "", ""},
		{"too few parts", "00-abc", ""},
		{"bad length", "00-tooshort-b7ad6b7169203331-01", ""},
		{"non-hex", "00-zzf7651916cd43dd8448eb211c80319c-b7ad6b7169203331-01", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := traceIDFromTraceparent(tc.in); got != tc.want {
				t.Errorf("traceIDFromTraceparent(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}
