package otel

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"go.opentelemetry.io/otel/sdk/resource"
)

func TestNewProviders(t *testing.T) {
	if _, err := NewProviders(context.Background(), ""); err == nil {
		t.Fatal("expected error for empty service name")
	}

	t.Run("resource build error", func(t *testing.T) {
		original := newResource
		t.Cleanup(func() { newResource = original })

		wantErr := errors.New("boom")
		newResource = func(context.Context, ...resource.Option) (*resource.Resource, error) {
			return nil, wantErr
		}

		if _, err := NewProviders(context.Background(), "test-service"); !errors.Is(err, wantErr) {
			t.Fatalf("expected wrapped error, got %v", err)
		}
	})

	providers, err := NewProviders(context.Background(), "test-service")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if providers.Tracer == nil || providers.Meter == nil {
		t.Fatal("expected non-nil tracer and meter providers")
	}

	if err := providers.Shutdown(context.Background()); err != nil {
		t.Fatalf("unexpected shutdown error: %v", err)
	}
}

func TestProviders_ShutdownErrors(t *testing.T) {
	wantErr := errors.New("boom")

	tracerErrs := &Providers{
		shutdownTracer: func(context.Context) error { return wantErr },
		shutdownMeter:  func(context.Context) error { return nil },
	}
	if err := tracerErrs.Shutdown(context.Background()); !errors.Is(err, wantErr) {
		t.Fatalf("expected wrapped tracer error, got %v", err)
	}

	meterErrs := &Providers{
		shutdownTracer: func(context.Context) error { return nil },
		shutdownMeter:  func(context.Context) error { return wantErr },
	}
	if err := meterErrs.Shutdown(context.Background()); !errors.Is(err, wantErr) {
		t.Fatalf("expected wrapped meter error, got %v", err)
	}
}

func TestMiddleware(t *testing.T) {
	called := false
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.WriteHeader(http.StatusOK)
	})

	handler := Middleware("test.operation", next)

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if !called {
		t.Fatal("expected wrapped handler to be called")
	}
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
}
