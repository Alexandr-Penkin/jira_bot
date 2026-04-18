package identity

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"

	"github.com/rs/zerolog"

	identityv1 "SleepJiraBot/pkg/identityv1"
)

// Server wraps a Provider with an HTTP surface. Register the handler on
// an internal-network-only listener — the AuthToken is a coarse defence
// against misconfigured ingress, not an authorisation boundary.
type Server struct {
	provider  Provider
	authToken string
	log       zerolog.Logger
}

func NewServer(provider Provider, authToken string, log zerolog.Logger) *Server {
	return &Server{provider: provider, authToken: authToken, log: log}
}

// Handler returns an http.Handler that serves the lease protocol. Only
// POST LeasePath is handled; every other request returns 404 so the
// listener can be safely shared with an internal debug endpoint.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc(identityv1.LeasePath, s.serveLease)
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})
	return mux
}

func (s *Server) serveLease(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if !s.authorized(r) {
		writeError(w, http.StatusUnauthorized, identityv1.ErrCodeUnauthorized, "missing or invalid bearer token")
		return
	}

	defer func() { _ = r.Body.Close() }()
	body, err := io.ReadAll(io.LimitReader(r.Body, 4096))
	if err != nil {
		writeError(w, http.StatusBadRequest, identityv1.ErrCodeInvalidRequest, "read body: "+err.Error())
		return
	}

	var req identityv1.TokenLeaseRequest
	if err := json.Unmarshal(body, &req); err != nil {
		writeError(w, http.StatusBadRequest, identityv1.ErrCodeInvalidRequest, "decode body: "+err.Error())
		return
	}

	resp, err := s.provider.Lease(r.Context(), req)
	if err != nil {
		var leaseErr *LeaseError
		if errors.As(err, &leaseErr) {
			writeError(w, statusForCode(leaseErr.Code), leaseErr.Code, leaseErr.Message)
			return
		}
		s.log.Error().Err(err).Int64("telegram_id", req.TelegramID).Msg("identity: lease failed")
		writeError(w, http.StatusInternalServerError, identityv1.ErrCodeInternal, err.Error())
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	// The response payload intentionally carries an access token — that's
	// the whole point of the lease endpoint. The listener is network-
	// isolated and protected by a bearer token, see authorized().
	_ = json.NewEncoder(w).Encode(resp) //nolint:gosec // G117: access_token is the intended payload
}

func (s *Server) authorized(r *http.Request) bool {
	if s.authToken == "" {
		// No token configured — server is effectively open. The caller
		// is expected to protect the listener at the network layer. We
		// log a warning once at startup; here we accept the request.
		return true
	}
	header := r.Header.Get(identityv1.AuthHeader)
	if !strings.HasPrefix(header, identityv1.AuthScheme) {
		return false
	}
	return strings.TrimPrefix(header, identityv1.AuthScheme) == s.authToken
}

func statusForCode(code string) int {
	switch code {
	case identityv1.ErrCodeNotConnected:
		return http.StatusNotFound
	case identityv1.ErrCodeInvalidRefreshToken:
		return http.StatusUnauthorized
	case identityv1.ErrCodeRefreshFailed:
		return http.StatusBadGateway
	case identityv1.ErrCodeInvalidRequest:
		return http.StatusBadRequest
	case identityv1.ErrCodeUnauthorized:
		return http.StatusUnauthorized
	default:
		return http.StatusInternalServerError
	}
}

func writeError(w http.ResponseWriter, status int, code, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(identityv1.ErrorResponse{Code: code, Message: message})
}
