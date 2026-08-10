package main

import (
	"testing"
	"time"
)

func TestPollInterval(t *testing.T) {
	t.Run("default when unset", func(t *testing.T) {
		t.Setenv("HTR_REDACTION_AUDIT_INTERVAL", "")
		if got := pollInterval(); got != 5*time.Minute {
			t.Fatalf("got %v, want 5m", got)
		}
	})

	t.Run("parses a valid duration", func(t *testing.T) {
		t.Setenv("HTR_REDACTION_AUDIT_INTERVAL", "30s")
		if got := pollInterval(); got != 30*time.Second {
			t.Fatalf("got %v, want 30s", got)
		}
	})

	t.Run("falls back to default on invalid duration", func(t *testing.T) {
		t.Setenv("HTR_REDACTION_AUDIT_INTERVAL", "not-a-duration")
		if got := pollInterval(); got != 5*time.Minute {
			t.Fatalf("got %v, want default 5m", got)
		}
	})
}

func TestAlertSink(t *testing.T) {
	t.Run("requires HTR_ALERT_WEBHOOK_URL", func(t *testing.T) {
		t.Setenv("HTR_ALERT_WEBHOOK_URL", "")
		if _, err := alertSink(); err == nil {
			t.Fatal("expected error when webhook URL is unset")
		}
	})

	t.Run("builds a webhook sink", func(t *testing.T) {
		t.Setenv("HTR_ALERT_WEBHOOK_URL", "https://example.com/hook")
		sink, err := alertSink()
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if sink == nil {
			t.Fatal("expected a non-nil sink")
		}
	})
}

func TestErrRequiredEnv(t *testing.T) {
	err := errRequiredEnv("HTR_FOO")
	if err.Error() == "" {
		t.Fatal("expected non-empty error message")
	}
}
