// Package handlers provides HTTP handlers for the Reign REST API.
package handlers

import (
	"database/sql"
	"encoding/json"
	"net/http"

	"github.com/reign/internal/orchestrator"
)

// version is set at build time
var version = "dev"

// SetVersion sets the version string for health responses.
func SetVersion(v string) {
	version = v
}

// HealthHandler handles health check requests.
type HealthHandler struct {
	db           *sql.DB
	orchestrator *orchestrator.Orchestrator
}

// NewHealthHandler creates a new HealthHandler.
func NewHealthHandler(db *sql.DB, orch *orchestrator.Orchestrator) *HealthHandler {
	return &HealthHandler{
		db:           db,
		orchestrator: orch,
	}
}

// HealthResponse represents the health check response.
type HealthResponse struct {
	Status          string `json:"status"`
	Version         string `json:"version"`
	Uptime          int64  `json:"uptime_seconds"`
	ServicesTotal   int    `json:"services_total"`
	ServicesRunning int    `json:"services_running"`
	ServicesFailed  int    `json:"services_failed"`
}

// ServeHTTP handles GET /health requests.
func (h *HealthHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	stats, err := h.orchestrator.GetStats(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to get stats")
		return
	}

	resp := HealthResponse{
		Status:          "ok",
		Version:         version,
		Uptime:          stats.Uptime,
		ServicesTotal:   stats.ServicesTotal,
		ServicesRunning: stats.ServicesRunning,
		ServicesFailed:  stats.ServicesFailed,
	}

	writeJSON(w, http.StatusOK, resp)
}

// ErrorResponse represents an error response.
type ErrorResponse struct {
	Error   string `json:"error"`
	Code    string `json:"code,omitempty"`
	Details string `json:"details,omitempty"`
}

// writeJSON writes a JSON response.
func writeJSON(w http.ResponseWriter, status int, data interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(data)
}

// writeError writes an error response.
func writeError(w http.ResponseWriter, status int, message string) {
	writeJSON(w, status, ErrorResponse{Error: message})
}

// writeErrorWithCode writes an error response with a code.
func writeErrorWithCode(w http.ResponseWriter, status int, code, message string) {
	writeJSON(w, status, ErrorResponse{Error: message, Code: code})
}
