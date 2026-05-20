package health

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestLiveness_AlwaysOK(t *testing.T) {
	mux := Mux()
	req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	rec := httptest.NewRecorder()

	mux.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, "ok", rec.Body.String())
}

func TestReadiness_AllChecksPass(t *testing.T) {
	mux := Mux(
		Probe{Name: "a", Check: func(context.Context) error { return nil }},
		Probe{Name: "b", Check: func(context.Context) error { return nil }},
	)
	req := httptest.NewRequest(http.MethodGet, "/readyz", nil)
	rec := httptest.NewRecorder()

	mux.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)
	assert.Contains(t, rec.Body.String(), `"status":"ok"`)
	assert.Contains(t, rec.Body.String(), `"a":"ok"`)
	assert.Contains(t, rec.Body.String(), `"b":"ok"`)
}

func TestReadiness_AnyFailureReturns503(t *testing.T) {
	mux := Mux(
		Probe{Name: "mongo", Check: func(context.Context) error { return nil }},
		Probe{Name: "nats", Check: func(context.Context) error { return errors.New("not connected") }},
	)
	req := httptest.NewRequest(http.MethodGet, "/readyz", nil)
	rec := httptest.NewRecorder()

	mux.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusServiceUnavailable, rec.Code)
	assert.Contains(t, rec.Body.String(), `"status":"unready"`)
	assert.Contains(t, rec.Body.String(), `"nats":"not connected"`)
}

func TestReadiness_NoProbesIsOK(t *testing.T) {
	// A degenerate but legal case: no probes registered → /readyz
	// trivially returns 200. Lets a service expose the endpoint
	// even before its dependencies are wired up.
	mux := Mux()
	req := httptest.NewRequest(http.MethodGet, "/readyz", nil)
	rec := httptest.NewRecorder()

	mux.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)
}
