package handlers

import (
	"database/sql"
	"net/http"

	"github.com/reign/internal/doctor"
	"github.com/reign/internal/models"
)

// DoctorHandler reports compose projects and systemd units that conflict with reign.
type DoctorHandler struct {
	db     *sql.DB
	doctor *doctor.Doctor
}

// NewDoctorHandler creates a new DoctorHandler.
func NewDoctorHandler(db *sql.DB, d *doctor.Doctor) *DoctorHandler {
	return &DoctorHandler{db: db, doctor: d}
}

// ServeHTTP handles GET /doctor requests.
func (h *DoctorHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	services, err := models.ListServices(h.db)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to list services")
		return
	}
	writeJSON(w, http.StatusOK, h.doctor.Check(r.Context(), services))
}
