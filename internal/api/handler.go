package api

import (
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/google/uuid"

	"github.com/devguy201-9/auto-service-scheduler/internal/repository"
	"github.com/devguy201-9/auto-service-scheduler/internal/service"
)

type Handler struct {
	booking *service.BookingService
	repo    *repository.Repository
	log     *slog.Logger
}

func NewHandler(booking *service.BookingService, repo *repository.Repository, log *slog.Logger) *Handler {
	return &Handler{booking: booking, repo: repo, log: log}
}

func (h *Handler) Routes() http.Handler {
	r := chi.NewRouter()
	r.Use(middleware.RequestID)
	r.Use(middleware.Recoverer)

	r.Get("/healthz", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) })
	r.Get("/readyz", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) })

	r.Route("/api/v1", func(r chi.Router) {
		r.Post("/appointments", h.createAppointment)
		r.Get("/appointments/{id}", h.getAppointment)
	})
	return r
}

type createAppointmentRequest struct {
	CustomerID    string `json:"customer_id"`
	VehicleID     string `json:"vehicle_id"`
	DealershipID  string `json:"dealership_id"`
	ServiceTypeID string `json:"service_type_id"`
	DesiredStart  string `json:"desired_start"` // RFC3339
}

func (h *Handler) createAppointment(w http.ResponseWriter, r *http.Request) {
	var req createAppointmentRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}

	customerID, e1 := uuid.Parse(req.CustomerID)
	vehicleID, e2 := uuid.Parse(req.VehicleID)
	dealershipID, e3 := uuid.Parse(req.DealershipID)
	serviceTypeID, e4 := uuid.Parse(req.ServiceTypeID)
	start, e5 := time.Parse(time.RFC3339, req.DesiredStart)
	if e1 != nil || e2 != nil || e3 != nil || e4 != nil || e5 != nil {
		writeError(w, http.StatusBadRequest, "invalid id or timestamp")
		return
	}

	appt, err := h.booking.Book(r.Context(), service.BookRequest{
		CustomerID:    customerID,
		VehicleID:     vehicleID,
		DealershipID:  dealershipID,
		ServiceTypeID: serviceTypeID,
		DesiredStart:  start,
	})
	switch {
	case errors.Is(err, repository.ErrNoAvailability):
		writeError(w, http.StatusConflict, "no bay and/or qualified technician available")
		return
	case errors.Is(err, repository.ErrServiceTypeNotFound):
		writeError(w, http.StatusBadRequest, "unknown service type")
		return
	case err != nil:
		h.log.Error("booking failed", "err", err)
		writeError(w, http.StatusInternalServerError, "internal error")
		return
	}

	writeJSON(w, http.StatusCreated, appointmentView(appt))
}

func (h *Handler) getAppointment(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid id")
		return
	}
	appt, err := h.repo.GetAppointment(r.Context(), id)
	if errors.Is(err, repository.ErrAppointmentNotFound) {
		writeError(w, http.StatusNotFound, "appointment not found")
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	writeJSON(w, http.StatusOK, appointmentView(appt))
}
