package profiling

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestDisabledByDefault(t *testing.T) {
	t.Setenv("PPROF_ENABLED", "")

	if Enabled() {
		t.Fatal("Enabled() = true with PPROF_ENABLED unset, want false")
	}
	if srv := Start(); srv != nil {
		Shutdown(srv)
		t.Fatal("Start() returned a server while profiling is disabled")
	}
}

func TestNotEnabledForNonTrueValues(t *testing.T) {
	for _, v := range []string{"1", "yes", "TRUE", "on", "false"} {
		t.Setenv("PPROF_ENABLED", v)
		if Enabled() {
			t.Errorf("Enabled() = true for PPROF_ENABLED=%q, want false (only exact \"true\" enables)", v)
		}
	}
}

func TestAddrDefaultsToLoopback(t *testing.T) {
	t.Setenv("PPROF_ADDR", "")
	if got := Addr(); got != DefaultAddr {
		t.Errorf("Addr() = %q, want loopback default %q", got, DefaultAddr)
	}
	if DefaultAddr[:9] != "127.0.0.1" {
		t.Errorf("DefaultAddr %q does not bind loopback — profiling could be publicly reachable", DefaultAddr)
	}
}

func TestHandlerServesPprofWhenEnabled(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/debug/pprof/", nil)
	rec := httptest.NewRecorder()

	Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("GET /debug/pprof/ = %d, want 200", rec.Code)
	}
}

func TestHandlerHasNoRouteOutsidePprof(t *testing.T) {
	// The pprof handler must not answer arbitrary API paths.
	req := httptest.NewRequest(http.MethodGet, "/v1/events", nil)
	rec := httptest.NewRecorder()

	Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Errorf("GET /v1/events on pprof mux = %d, want 404", rec.Code)
	}
}
