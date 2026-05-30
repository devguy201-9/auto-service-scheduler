//go:build integration

package test

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"os"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/devguy201-9/auto-service-scheduler/internal/repository"
	"github.com/devguy201-9/auto-service-scheduler/internal/service"
)

func TestConcurrentBooking_NoDoubleBooking(t *testing.T) {
	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		dsn = "postgres://scheduler:scheduler@localhost:5432/scheduler?sslmode=disable"
	}
	ctx := context.Background()

	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer pool.Close()

	// Clean up database for deterministic test results
	if _, err := pool.Exec(ctx, "DELETE FROM appointment"); err != nil {
		t.Fatalf("cleanup: %v", err)
	}

	repo := repository.New(pool)
	svc := service.NewBookingService(repo, slog.New(slog.NewTextHandler(io.Discard, nil)))

	const n = 20
	req := service.BookRequest{
		CustomerID:    uuid.MustParse("22222222-2222-2222-2222-222222222222"),
		VehicleID:     uuid.MustParse("33333333-3333-3333-3333-333333333333"),
		DealershipID:  uuid.MustParse("11111111-1111-1111-1111-111111111111"),
		ServiceTypeID: uuid.MustParse("88888888-8888-8888-8888-888888888888"), // Brake Service: assigned to Alice
		DesiredStart:  time.Date(2026, 6, 1, 9, 0, 0, 0, time.UTC),
	}

	var success, conflict int64
	var wg sync.WaitGroup
	wg.Add(n)
	for i := 0; i < n; i++ {
		go func() {
			defer wg.Done()
			_, err := svc.Book(ctx, req)
			switch {
			case err == nil:
				atomic.AddInt64(&success, 1)
			case errors.Is(err, repository.ErrNoAvailability):
				atomic.AddInt64(&conflict, 1)
			default:
				t.Errorf("unexpected error: %v", err)
			}
		}()
	}
	wg.Wait()

	if success != 1 {
		t.Fatalf("expected exactly 1 success, got %d (conflicts=%d)", success, conflict)
	}
	t.Logf("OK: 1 success, %d conflicts out of %d", conflict, n)
}
