// Package health serves /healthz (liveness, always 200 while the process
// is up) and /readyz (readiness, runs a set of dependency probes and
// returns 503 when any fail). Each *-svc binary wires its own probe set
// — typically Mongo ping and NATS connection — so a half-broken pod is
// taken out of K8s rotation instead of receiving traffic.
package health

import (
	"context"
	"encoding/json"
	"net/http"
	"sync"
	"time"
)

// CheckFunc reports whether a single dependency is healthy. It must
// honour ctx — readiness handlers cap the entire probe set at
// probeTimeout, and a misbehaving probe shouldn't tie up the responder.
type CheckFunc func(ctx context.Context) error

// Probe pairs a CheckFunc with a stable name so /readyz can name the
// failing dependency in its response body.
type Probe struct {
	Name  string
	Check CheckFunc
}

const probeTimeout = 2 * time.Second

// Mux returns an http.Handler exposing /healthz and /readyz. Mount it at
// the root of a service's HTTP server, or compose it with the service's
// own routes via http.ServeMux.
//
// /healthz returns 200 unconditionally — it answers "is the process
// up?" not "is it useful?". /readyz runs every probe in parallel,
// each capped at probeTimeout, and returns 503 if any fail.
func Mux(probes ...Probe) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", liveness)
	mux.Handle("/readyz", Readiness(probes...))
	return mux
}

func liveness(w http.ResponseWriter, _ *http.Request) {
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte("ok"))
}

// Readiness exposes /readyz on its own — for services that need to wire
// it into an existing mux rather than mount the whole Mux above.
func Readiness(probes ...Probe) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), probeTimeout)
		defer cancel()

		type result struct {
			name string
			err  error
		}
		results := make([]result, len(probes))

		var wg sync.WaitGroup
		for i, p := range probes {
			wg.Add(1)
			go func(i int, p Probe) {
				defer wg.Done()
				err := p.Check(ctx)
				results[i] = result{name: p.Name, err: err}
			}(i, p)
		}
		wg.Wait()

		body := struct {
			Status string            `json:"status"`
			Checks map[string]string `json:"checks"`
		}{
			Status: "ok",
			Checks: make(map[string]string, len(results)),
		}
		anyFailed := false
		for _, r := range results {
			if r.err != nil {
				anyFailed = true
				body.Checks[r.name] = r.err.Error()
			} else {
				body.Checks[r.name] = "ok"
			}
		}
		if anyFailed {
			body.Status = "unready"
		}

		w.Header().Set("Content-Type", "application/json")
		if anyFailed {
			w.WriteHeader(http.StatusServiceUnavailable)
		} else {
			w.WriteHeader(http.StatusOK)
		}
		_ = json.NewEncoder(w).Encode(body)
	})
}
