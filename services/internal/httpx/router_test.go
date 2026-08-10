package httpx

import (
	"bytes"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestNewRouter_ServesAndLogs(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&buf, nil))

	r := NewRouter("test-service", logger)
	r.Get("/ping", func(w http.ResponseWriter, req *http.Request) {
		w.WriteHeader(http.StatusTeapot)
	})

	req := httptest.NewRequest(http.MethodGet, "/ping", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusTeapot {
		t.Fatalf("expected 418, got %d", rec.Code)
	}

	logged := buf.String()
	if !strings.Contains(logged, "http_request") || !strings.Contains(logged, "status=418") {
		t.Fatalf("expected request log entry, got %q", logged)
	}
}

func TestNewRouter_DefaultLogger(t *testing.T) {
	r := NewRouter("test-service", nil)
	r.Get("/ok", func(w http.ResponseWriter, req *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	req := httptest.NewRequest(http.MethodGet, "/ok", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
}

func TestNewRouter_Recovers(t *testing.T) {
	r := NewRouter("test-service", nil)
	r.Get("/boom", func(w http.ResponseWriter, req *http.Request) {
		panic("boom")
	})

	req := httptest.NewRequest(http.MethodGet, "/boom", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("expected recovered 500, got %d", rec.Code)
	}
}
