package api

import (
	"encoding/json"
	"net/http"
	"time"

	"github.com/devguy201-9/auto-service-scheduler/internal/domain"
)

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}

func appointmentView(a domain.Appointment) map[string]any {
	return map[string]any{
		"id":              a.ID,
		"dealership_id":   a.DealershipID,
		"customer_id":     a.CustomerID,
		"vehicle_id":      a.VehicleID,
		"service_type_id": a.ServiceTypeID,
		"technician_id":   a.TechnicianID,
		"service_bay_id":  a.ServiceBayID,
		"start_time":      a.Window.Start.Format(time.RFC3339),
		"end_time":        a.Window.End.Format(time.RFC3339),
		"status":          a.Status,
	}
}

func windowView(w domain.TimeRange) map[string]string {
	return map[string]string{
		"start": w.Start.Format(time.RFC3339),
		"end":   w.End.Format(time.RFC3339),
	}
}
