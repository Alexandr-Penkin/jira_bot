package preferences

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"

	"github.com/rs/zerolog"

	preferencesv1 "SleepJiraBot/pkg/preferencesv1"
)

// Server exposes Provider over HTTP. Register on an internal-only
// listener — AuthToken is a coarse defence, not authorisation.
type Server struct {
	provider  Provider
	authToken string
	log       zerolog.Logger
}

func NewServer(provider Provider, authToken string, log zerolog.Logger) *Server {
	return &Server{provider: provider, authToken: authToken, log: log}
}

// Handler returns an http.Handler that serves the preferences protocol.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc(preferencesv1.GetPath, s.serveGet)
	mux.HandleFunc(preferencesv1.SetLanguagePath, s.serveSetLanguage)
	mux.HandleFunc(preferencesv1.SetDefaultsPath, s.serveSetDefaults)
	mux.HandleFunc(preferencesv1.SetSprintIssueTypesPath, s.serveSetSprintIssueTypes)
	mux.HandleFunc(preferencesv1.SetDoneStatusesPath, s.serveSetDoneStatuses)
	mux.HandleFunc(preferencesv1.SetHoldStatusesPath, s.serveSetHoldStatuses)
	mux.HandleFunc(preferencesv1.SetAssigneeFieldPath, s.serveSetAssigneeField)
	mux.HandleFunc(preferencesv1.SetStoryPointsFieldPath, s.serveSetStoryPointsField)
	mux.HandleFunc(preferencesv1.SetDailyJQLPath, s.serveSetDailyJQL)
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})
	return mux
}

func (s *Server) serveGet(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if !s.authorized(r) {
		writeError(w, http.StatusUnauthorized, preferencesv1.ErrCodeUnauthorized, "missing or invalid bearer token")
		return
	}
	raw := r.URL.Query().Get("telegram_id")
	id, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || id == 0 {
		writeError(w, http.StatusBadRequest, preferencesv1.ErrCodeInvalidRequest, "telegram_id query param is required")
		return
	}
	prefs, err := s.provider.Get(r.Context(), id)
	if err != nil {
		s.writeProviderError(w, r, err, "get")
		return
	}
	writeJSON(w, http.StatusOK, prefs)
}

func (s *Server) serveSetLanguage(w http.ResponseWriter, r *http.Request) {
	var req preferencesv1.SetLanguageRequest
	if !s.decodePost(w, r, &req) {
		return
	}
	if err := s.provider.SetLanguage(r.Context(), req.TelegramID, req.Language); err != nil {
		s.writeProviderError(w, r, err, "set_language")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) serveSetDefaults(w http.ResponseWriter, r *http.Request) {
	var req preferencesv1.SetDefaultsRequest
	if !s.decodePost(w, r, &req) {
		return
	}
	if err := s.provider.SetDefaults(r.Context(), req.TelegramID, req.DefaultProject, req.DefaultBoardID); err != nil {
		s.writeProviderError(w, r, err, "set_defaults")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) serveSetSprintIssueTypes(w http.ResponseWriter, r *http.Request) {
	var req preferencesv1.SetSprintIssueTypesRequest
	if !s.decodePost(w, r, &req) {
		return
	}
	if err := s.provider.SetSprintIssueTypes(r.Context(), req.TelegramID, req.IssueTypes); err != nil {
		s.writeProviderError(w, r, err, "set_sprint_issue_types")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) serveSetDoneStatuses(w http.ResponseWriter, r *http.Request) {
	var req preferencesv1.SetStatusesRequest
	if !s.decodePost(w, r, &req) {
		return
	}
	if err := s.provider.SetDoneStatuses(r.Context(), req.TelegramID, req.Statuses); err != nil {
		s.writeProviderError(w, r, err, "set_done_statuses")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) serveSetHoldStatuses(w http.ResponseWriter, r *http.Request) {
	var req preferencesv1.SetStatusesRequest
	if !s.decodePost(w, r, &req) {
		return
	}
	if err := s.provider.SetHoldStatuses(r.Context(), req.TelegramID, req.Statuses); err != nil {
		s.writeProviderError(w, r, err, "set_hold_statuses")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) serveSetAssigneeField(w http.ResponseWriter, r *http.Request) {
	var req preferencesv1.SetFieldRequest
	if !s.decodePost(w, r, &req) {
		return
	}
	if err := s.provider.SetAssigneeField(r.Context(), req.TelegramID, req.FieldID); err != nil {
		s.writeProviderError(w, r, err, "set_assignee_field")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) serveSetStoryPointsField(w http.ResponseWriter, r *http.Request) {
	var req preferencesv1.SetFieldRequest
	if !s.decodePost(w, r, &req) {
		return
	}
	if err := s.provider.SetStoryPointsField(r.Context(), req.TelegramID, req.FieldID); err != nil {
		s.writeProviderError(w, r, err, "set_story_points_field")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) serveSetDailyJQL(w http.ResponseWriter, r *http.Request) {
	var req preferencesv1.SetDailyJQLRequest
	if !s.decodePost(w, r, &req) {
		return
	}
	if err := s.provider.SetDailyJQL(r.Context(), req.TelegramID, req.DoneJQL, req.DoingJQL, req.PlanJQL); err != nil {
		s.writeProviderError(w, r, err, "set_daily_jql")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// decodePost handles the shared bits of POST endpoints: method, auth,
// body decode. Returns false if the caller should stop.
func (s *Server) decodePost(w http.ResponseWriter, r *http.Request, out any) bool {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return false
	}
	if !s.authorized(r) {
		writeError(w, http.StatusUnauthorized, preferencesv1.ErrCodeUnauthorized, "missing or invalid bearer token")
		return false
	}
	defer func() { _ = r.Body.Close() }()
	body, err := io.ReadAll(io.LimitReader(r.Body, 64*1024))
	if err != nil {
		writeError(w, http.StatusBadRequest, preferencesv1.ErrCodeInvalidRequest, "read body: "+err.Error())
		return false
	}
	if err := json.Unmarshal(body, out); err != nil {
		writeError(w, http.StatusBadRequest, preferencesv1.ErrCodeInvalidRequest, "decode body: "+err.Error())
		return false
	}
	return true
}

func (s *Server) authorized(r *http.Request) bool {
	if s.authToken == "" {
		return true
	}
	header := r.Header.Get(preferencesv1.AuthHeader)
	if !strings.HasPrefix(header, preferencesv1.AuthScheme) {
		return false
	}
	return strings.TrimPrefix(header, preferencesv1.AuthScheme) == s.authToken
}

func (s *Server) writeProviderError(w http.ResponseWriter, r *http.Request, err error, op string) {
	var provErr *ProviderError
	if errors.As(err, &provErr) {
		writeError(w, statusForCode(provErr.Code), provErr.Code, provErr.Message)
		return
	}
	s.log.Error().Err(err).Str("op", op).Str("path", r.URL.Path).Msg("preferences: internal error")
	writeError(w, http.StatusInternalServerError, preferencesv1.ErrCodeInternal, err.Error())
}

func statusForCode(code string) int {
	switch code {
	case preferencesv1.ErrCodeNotFound:
		return http.StatusNotFound
	case preferencesv1.ErrCodeInvalidRequest:
		return http.StatusBadRequest
	case preferencesv1.ErrCodeUnauthorized:
		return http.StatusUnauthorized
	default:
		return http.StatusInternalServerError
	}
}

func writeError(w http.ResponseWriter, status int, code, message string) {
	writeJSON(w, status, preferencesv1.ErrorResponse{Code: code, Message: message})
}

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}
