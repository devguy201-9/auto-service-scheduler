//go:build integration

package test

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/devguy201-9/auto-service-scheduler/internal/api"
	"github.com/devguy201-9/auto-service-scheduler/internal/repository"
	"github.com/devguy201-9/auto-service-scheduler/internal/service"
)

const (
	testDealershipID  = "11111111-1111-1111-1111-111111111111"
	testCustomerID    = "22222222-2222-2222-2222-222222222222"
	testVehicleID     = "33333333-3333-3333-3333-333333333333"
	testBrakeServiceID = "88888888-8888-8888-8888-888888888888"
)

func connectTestPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		dsn = "postgres://scheduler:scheduler@localhost:5432/scheduler?sslmode=disable"
	}
	pool, err := pgxpool.New(context.Background(), dsn)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	t.Cleanup(pool.Close)
	return pool
}

func cleanAppointments(t *testing.T, pool *pgxpool.Pool) {
	t.Helper()
	if _, err := pool.Exec(context.Background(), "DELETE FROM appointment"); err != nil {
		t.Fatalf("cleanup: %v", err)
	}
}

func buildAvailabilityServer(t *testing.T, pool *pgxpool.Pool) *httptest.Server {
	t.Helper()
	repo := repository.New(pool)
	svc := service.NewBookingService(repo, slog.New(slog.NewTextHandler(io.Discard, nil)))
	h := api.NewHandler(svc, repo, slog.New(slog.NewTextHandler(io.Discard, nil)))
	srv := httptest.NewServer(h.Routes())
	t.Cleanup(srv.Close)
	return srv
}

func setupBooking(t *testing.T, pool *pgxpool.Pool, start time.Time) {
	t.Helper()
	repo := repository.New(pool)
	svc := service.NewBookingService(repo, slog.New(slog.NewTextHandler(io.Discard, nil)))
	_, err := svc.Book(context.Background(), service.BookRequest{
		CustomerID:    uuid.MustParse(testCustomerID),
		VehicleID:     uuid.MustParse(testVehicleID),
		DealershipID:  uuid.MustParse(testDealershipID),
		ServiceTypeID: uuid.MustParse(testBrakeServiceID),
		DesiredStart:  start,
	})
	if err != nil {
		t.Fatalf("setup booking: %v", err)
	}
}

func getAvailability(t *testing.T, srv *httptest.Server, start string) (int, map[string]any) {
	t.Helper()
	url := fmt.Sprintf("%s/api/v1/dealerships/%s/availability?service_type_id=%s&start=%s",
		srv.URL, testDealershipID, testBrakeServiceID, start)
	resp, err := http.Get(url) //nolint:noctx
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer resp.Body.Close()
	var body map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	return resp.StatusCode, body
}

func TestAvailability_Available(t *testing.T) {
	pool := connectTestPool(t)
	cleanAppointments(t, pool)
	srv := buildAvailabilityServer(t, pool)

	code, body := getAvailability(t, srv, "2026-06-01T11:00:00Z")

	if code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %v", code, body)
	}
	if body["available"] != true {
		t.Errorf("expected available=true, got %v", body)
	}
	t.Logf("OK: %v", body)
}

func TestAvailability_NoQualifiedTechnician(t *testing.T) {
	pool := connectTestPool(t)
	cleanAppointments(t, pool)
	srv := buildAvailabilityServer(t, pool)

	// Book Brake Service at 09:00 — window [09:00, 10:00) consumes Alice, the only brake tech.
	setupBooking(t, pool, time.Date(2026, 6, 1, 9, 0, 0, 0, time.UTC))

	// 09:30 window [09:30, 10:30) overlaps; Bay 2 is free but no brake tech remains.
	code, body := getAvailability(t, srv, "2026-06-01T09:30:00Z")

	if code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %v", code, body)
	}
	if body["available"] != false {
		t.Errorf("expected available=false, got %v", body)
	}
	if body["reason"] != "no_qualified_technician" {
		t.Errorf("expected reason=no_qualified_technician, got %v", body["reason"])
	}
	t.Logf("OK: %v", body)
}

func TestAvailability_AdjacentSlotIsAvailable(t *testing.T) {
	pool := connectTestPool(t)
	cleanAppointments(t, pool)
	srv := buildAvailabilityServer(t, pool)

	// Book Brake Service at 09:00 — window [09:00, 10:00).
	setupBooking(t, pool, time.Date(2026, 6, 1, 9, 0, 0, 0, time.UTC))

	// 10:00 window [10:00, 11:00) is adjacent — half-open interval, must not conflict.
	code, body := getAvailability(t, srv, "2026-06-01T10:00:00Z")

	if code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %v", code, body)
	}
	if body["available"] != true {
		t.Errorf("expected available=true, got %v", body)
	}
	t.Logf("OK: %v", body)
}
