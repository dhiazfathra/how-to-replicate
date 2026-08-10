package audit

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestWebhookSink_Alert(t *testing.T) {
	var received Finding
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewDecoder(r.Body).Decode(&received); err != nil {
			t.Errorf("decode body: %v", err)
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	sink := NewWebhookSink(server.URL)
	applied := int32(1)
	finding := Finding{
		CaptureID:                "cap1",
		WorkspaceID:              "ws1",
		AppliedRulesetVersion:    &applied,
		EvaluationRulesetVersion: 2,
		RuleIDs:                  []string{"builtin:nik"},
		EventIDs:                 []string{"evt1"},
	}

	if err := sink.Alert(context.Background(), finding); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if received.CaptureID != "cap1" || received.EvaluationRulesetVersion != 2 {
		t.Fatalf("webhook did not receive expected finding: %+v", received)
	}
}

func TestWebhookSink_Alert_NonSuccessStatus(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer server.Close()

	sink := NewWebhookSink(server.URL)
	if err := sink.Alert(context.Background(), Finding{}); err == nil {
		t.Fatal("expected error for non-2xx response")
	}
}

func TestWebhookSink_Alert_RequestBuildFailure(t *testing.T) {
	sink := &WebhookSink{URL: "://not-a-url"}
	if err := sink.Alert(context.Background(), Finding{}); err == nil {
		t.Fatal("expected error for invalid URL")
	}
}

func TestWebhookSink_Alert_TransportFailure(t *testing.T) {
	sink := NewWebhookSink("http://127.0.0.1:0")
	if err := sink.Alert(context.Background(), Finding{}); err == nil {
		t.Fatal("expected error when nothing is listening")
	}
}

func TestWebhookSink_Alert_DefaultClientUsedWhenNil(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	sink := &WebhookSink{URL: server.URL}
	if err := sink.Alert(context.Background(), Finding{}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}
