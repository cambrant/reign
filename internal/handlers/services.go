package handlers

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/reign/internal/models"
	"github.com/reign/internal/orchestrator"
)

// ServicesHandler handles service-related requests.
type ServicesHandler struct {
	db           *sql.DB
	orchestrator *orchestrator.Orchestrator
}

// NewServicesHandler creates a new ServicesHandler.
func NewServicesHandler(db *sql.DB, orch *orchestrator.Orchestrator) *ServicesHandler {
	return &ServicesHandler{
		db:           db,
		orchestrator: orch,
	}
}

// ServiceRequest represents a request to create or update a service.
type ServiceRequest struct {
	ID             string `json:"id"`
	Name           string `json:"name"`
	Type           string `json:"type"`
	Path           string `json:"path"`
	Command        string `json:"command,omitempty"`
	Enabled        *bool  `json:"enabled,omitempty"`
	Infrastructure *bool  `json:"infrastructure,omitempty"`
}

// ServiceListResponse represents the response for listing services.
type ServiceListResponse struct {
	Services []models.ServiceWithState `json:"services"`
	Total    int                       `json:"total"`
}

// ServeHTTP routes requests to the appropriate handler.
func (h *ServicesHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	// Parse path to determine action
	path := strings.TrimPrefix(r.URL.Path, "/services")
	path = strings.TrimPrefix(path, "/")

	// Handle /services
	if path == "" {
		switch r.Method {
		case http.MethodGet:
			h.listServices(w, r)
		case http.MethodPost:
			h.createService(w, r)
		default:
			writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		}
		return
	}

	// Parse service ID and action
	parts := strings.SplitN(path, "/", 2)
	serviceID := parts[0]
	action := ""
	if len(parts) > 1 {
		action = parts[1]
	}

	switch action {
	case "":
		// /services/{id}
		switch r.Method {
		case http.MethodGet:
			h.getService(w, r, serviceID)
		case http.MethodPut:
			h.updateService(w, r, serviceID)
		case http.MethodDelete:
			h.deleteService(w, r, serviceID)
		default:
			writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		}
	case "start":
		if r.Method != http.MethodPost {
			writeError(w, http.StatusMethodNotAllowed, "method not allowed")
			return
		}
		h.startService(w, r, serviceID)
	case "stop":
		if r.Method != http.MethodPost {
			writeError(w, http.StatusMethodNotAllowed, "method not allowed")
			return
		}
		h.stopService(w, r, serviceID)
	case "restart":
		if r.Method != http.MethodPost {
			writeError(w, http.StatusMethodNotAllowed, "method not allowed")
			return
		}
		h.restartService(w, r, serviceID)
	case "logs":
		if r.Method != http.MethodGet {
			writeError(w, http.StatusMethodNotAllowed, "method not allowed")
			return
		}
		h.getServiceLogs(w, r, serviceID)
	case "stats":
		if r.Method != http.MethodGet {
			writeError(w, http.StatusMethodNotAllowed, "method not allowed")
			return
		}
		h.getServiceStats(w, r, serviceID)
	default:
		writeError(w, http.StatusNotFound, "not found")
	}
}

// listServices handles GET /services.
func (h *ServicesHandler) listServices(w http.ResponseWriter, r *http.Request) {
	services, err := models.ListServicesWithState(h.db)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to list services")
		return
	}

	if services == nil {
		services = []models.ServiceWithState{}
	}

	resp := ServiceListResponse{
		Services: services,
		Total:    len(services),
	}

	writeJSON(w, http.StatusOK, resp)
}

// getService handles GET /services/{id}.
func (h *ServicesHandler) getService(w http.ResponseWriter, r *http.Request, id string) {
	service, err := models.GetServiceWithState(h.db, id)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to get service")
		return
	}
	if service == nil {
		writeError(w, http.StatusNotFound, "service not found")
		return
	}

	// Include recent events
	events, err := models.GetServiceEvents(h.db, id, 20)
	if err != nil {
		events = []models.ServiceEvent{}
	}

	type ServiceDetailResponse struct {
		models.ServiceWithState
		Events []models.ServiceEvent `json:"events"`
	}

	resp := ServiceDetailResponse{
		ServiceWithState: *service,
		Events:           events,
	}

	writeJSON(w, http.StatusOK, resp)
}

// createService handles POST /services.
func (h *ServicesHandler) createService(w http.ResponseWriter, r *http.Request) {
	var req ServiceRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON")
		return
	}

	// Validate required fields
	if req.ID == "" {
		writeError(w, http.StatusBadRequest, "id is required")
		return
	}
	if req.Name == "" {
		writeError(w, http.StatusBadRequest, "name is required")
		return
	}
	if req.Type == "" {
		writeError(w, http.StatusBadRequest, "type is required")
		return
	}
	if req.Type != "compose" && req.Type != "binary" {
		writeError(w, http.StatusBadRequest, "type must be 'compose' or 'binary'")
		return
	}
	if req.Path == "" {
		writeError(w, http.StatusBadRequest, "path is required")
		return
	}

	// Check if service already exists
	existing, err := models.GetServiceByID(h.db, req.ID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to check existing service")
		return
	}
	if existing != nil {
		writeError(w, http.StatusConflict, "service already exists")
		return
	}

	// Create service
	service := &models.Service{
		ID:             req.ID,
		Name:           req.Name,
		Type:           models.ServiceType(req.Type),
		Path:           req.Path,
		Command:        req.Command,
		Enabled:        true,
		Infrastructure: false,
	}

	if req.Enabled != nil {
		service.Enabled = *req.Enabled
	}
	if req.Infrastructure != nil {
		service.Infrastructure = *req.Infrastructure
	}

	if err := service.Create(h.db); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to create service")
		return
	}

	models.LogEvent(h.db, req.ID, "created", "Service created")

	// Return created service with state
	created, err := models.GetServiceWithState(h.db, req.ID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to get created service")
		return
	}

	writeJSON(w, http.StatusCreated, created)
}

// updateService handles PUT /services/{id}.
func (h *ServicesHandler) updateService(w http.ResponseWriter, r *http.Request, id string) {
	service, err := models.GetServiceByID(h.db, id)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to get service")
		return
	}
	if service == nil {
		writeError(w, http.StatusNotFound, "service not found")
		return
	}

	var req ServiceRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON")
		return
	}

	// Update fields if provided
	if req.Name != "" {
		service.Name = req.Name
	}
	if req.Type != "" {
		if req.Type != "compose" && req.Type != "binary" {
			writeError(w, http.StatusBadRequest, "type must be 'compose' or 'binary'")
			return
		}
		service.Type = models.ServiceType(req.Type)
	}
	if req.Path != "" {
		service.Path = req.Path
	}
	if req.Enabled != nil {
		service.Enabled = *req.Enabled
		// Update state if disabling
		if !*req.Enabled {
			models.UpdateServiceStatus(h.db, id, models.StatusDisabled)
			models.LogEvent(h.db, id, "disabled", "Service disabled")
		} else {
			// If enabling, change status from disabled to stopped
			state, _ := models.GetServiceState(h.db, id)
			if state != nil && state.Status == models.StatusDisabled {
				models.UpdateServiceStatus(h.db, id, models.StatusStopped)
				models.LogEvent(h.db, id, "enabled", "Service enabled")
			}
		}
	}
	if req.Infrastructure != nil {
		service.Infrastructure = *req.Infrastructure
	}

	if err := service.Update(h.db); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to update service")
		return
	}

	models.LogEvent(h.db, id, "updated", "Service configuration updated")

	// Return updated service with state
	updated, err := models.GetServiceWithState(h.db, id)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to get updated service")
		return
	}

	writeJSON(w, http.StatusOK, updated)
}

// deleteService handles DELETE /services/{id}.
func (h *ServicesHandler) deleteService(w http.ResponseWriter, r *http.Request, id string) {
	service, err := models.GetServiceByID(h.db, id)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to get service")
		return
	}
	if service == nil {
		writeError(w, http.StatusNotFound, "service not found")
		return
	}

	// Stop service if running
	state, _ := models.GetServiceState(h.db, id)
	if state != nil && state.Status == models.StatusRunning {
		if err := h.orchestrator.StopService(r.Context(), id); err != nil {
			writeError(w, http.StatusInternalServerError, "failed to stop service before delete")
			return
		}
	}

	// Delete service
	if err := models.DeleteService(h.db, id); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to delete service")
		return
	}

	writeJSON(w, http.StatusOK, map[string]string{"status": "deleted"})
}

// startService handles POST /services/{id}/start.
func (h *ServicesHandler) startService(w http.ResponseWriter, r *http.Request, id string) {
	service, err := h.orchestrator.StartServiceAsync(id)
	if err != nil {
		if strings.Contains(err.Error(), "not found") {
			writeError(w, http.StatusNotFound, err.Error())
			return
		}
		if strings.Contains(err.Error(), "disabled") {
			writeErrorWithCode(w, http.StatusBadRequest, "SERVICE_DISABLED", err.Error())
			return
		}
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	writeJSON(w, http.StatusAccepted, service)
}

// stopService handles POST /services/{id}/stop.
func (h *ServicesHandler) stopService(w http.ResponseWriter, r *http.Request, id string) {
	service, err := h.orchestrator.StopServiceAsync(id)
	if err != nil {
		if strings.Contains(err.Error(), "not found") {
			writeError(w, http.StatusNotFound, err.Error())
			return
		}
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	writeJSON(w, http.StatusAccepted, service)
}

// restartService handles POST /services/{id}/restart.
func (h *ServicesHandler) restartService(w http.ResponseWriter, r *http.Request, id string) {
	service, err := h.orchestrator.RestartServiceAsync(id)
	if err != nil {
		if strings.Contains(err.Error(), "not found") {
			writeError(w, http.StatusNotFound, err.Error())
			return
		}
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	writeJSON(w, http.StatusAccepted, service)
}

// getServiceLogs handles GET /services/{id}/logs.
func (h *ServicesHandler) getServiceLogs(w http.ResponseWriter, r *http.Request, id string) {
	follow := r.URL.Query().Get("follow") == "true"

	lines := 100
	if linesParam := r.URL.Query().Get("lines"); linesParam != "" {
		if n, err := strconv.Atoi(linesParam); err == nil && n > 0 {
			lines = n
		}
	}

	if follow {
		h.streamServiceLogs(w, r, id, lines)
		return
	}

	logs, err := h.orchestrator.GetServiceLogs(r.Context(), id, lines)
	if err != nil {
		if strings.Contains(err.Error(), "not found") {
			writeError(w, http.StatusNotFound, err.Error())
			return
		}
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	writeJSON(w, http.StatusOK, map[string]string{"logs": logs})
}

// streamServiceLogs streams logs using Server-Sent Events.
func (h *ServicesHandler) streamServiceLogs(w http.ResponseWriter, r *http.Request, id string, lines int) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		writeError(w, http.StatusInternalServerError, "streaming not supported")
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.WriteHeader(http.StatusOK)
	flusher.Flush()

	ctx := r.Context()
	err := h.orchestrator.StreamServiceLogs(ctx, id, lines, func(line string) {
		fmt.Fprintf(w, "data: %s\n\n", line)
		flusher.Flush()
	})

	if err != nil && err != r.Context().Err() {
		fmt.Fprintf(w, "event: error\ndata: %s\n\n", err.Error())
		flusher.Flush()
	}
}

// EventLogResponse represents the response for the event log.
type EventLogResponse struct {
	Events []models.EventLogEntry `json:"events"`
	Total  int                    `json:"total"`
}

// getServiceStats handles GET /services/{id}/stats.
func (h *ServicesHandler) getServiceStats(w http.ResponseWriter, r *http.Request, id string) {
	stats, err := h.orchestrator.GetServiceStats(r.Context(), id)
	if err != nil {
		if strings.Contains(err.Error(), "not found") {
			writeError(w, http.StatusNotFound, err.Error())
			return
		}
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	writeJSON(w, http.StatusOK, stats)
}
