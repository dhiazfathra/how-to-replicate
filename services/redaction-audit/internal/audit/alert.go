package audit

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
)

// Finding is what gets paged to Security. Both ruleset versions are always
// present: AppliedRulesetVersion is what the client actually redacted with
// (nil if the capture predates ruleset versioning), EvaluationRulesetVersion
// is what this audit run evaluated against. Re-running the audit for the
// same capture with the same EvaluationRulesetVersion must reproduce the
// same Finding.
type Finding struct {
	CaptureID                string   `json:"captureId"`
	WorkspaceID              string   `json:"workspaceId"`
	AppliedRulesetVersion    *int32   `json:"appliedRulesetVersion"`
	EvaluationRulesetVersion int32    `json:"evaluationRulesetVersion"`
	RuleIDs                  []string `json:"ruleIds"`
	EventIDs                 []string `json:"eventIds"`
}

// Sink pages Security about a Finding. It is behind an interface so the
// vendor (PagerDuty, Opsgenie, a Slack webhook, ...) is a deployment
// choice, never a compile-time dependency of the audit logic itself.
type Sink interface {
	Alert(ctx context.Context, finding Finding) error
}

// WebhookSink posts a Finding as JSON to a single webhook URL — the
// smallest thing that can page Security without coupling to a specific
// vendor SDK. Any provider that accepts an inbound JSON webhook (which is
// effectively all of them) works behind this.
type WebhookSink struct {
	URL        string
	HTTPClient *http.Client
}

// NewWebhookSink returns a WebhookSink posting to url with http.DefaultClient.
func NewWebhookSink(url string) *WebhookSink {
	return &WebhookSink{URL: url, HTTPClient: http.DefaultClient}
}

// Alert posts finding to the configured webhook URL as JSON.
func (s *WebhookSink) Alert(ctx context.Context, finding Finding) error {
	// Finding is a plain struct of strings/slices/*int32 — it always
	// marshals; no error path to handle.
	body, _ := json.Marshal(finding)

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, s.URL, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("alert: build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	client := s.HTTPClient
	if client == nil {
		client = http.DefaultClient
	}

	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("alert: post webhook: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode >= 300 {
		return fmt.Errorf("alert: webhook returned status %d", resp.StatusCode)
	}
	return nil
}
