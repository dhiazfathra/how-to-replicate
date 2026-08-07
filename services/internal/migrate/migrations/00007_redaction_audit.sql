-- +goose Up
-- Task 12: redaction-audit findings. This table is the ONLY place the audit
-- writes about a capture — it never touches captures/capture_events/assets
-- themselves (invariant 7: the audit is an alarm, not a filter; the
-- unredacted content it detects has already crossed the network boundary,
-- so there is nothing left for it to redact). "Marking a capture for
-- review" means a finding row exists for it, discovered by joining this
-- table — not a status flag written onto the capture row.
--
-- Both ruleset versions are required on every finding: applied_ruleset_version
-- is what the client actually redacted with (captures.applied_ruleset_version,
-- Task 3), evaluation_ruleset_version is what THIS audit run evaluated
-- against. Without the pair, a finding raised after a ruleset update can't
-- be told apart from a genuine client-side redaction hole.
-- +goose StatementBegin
CREATE TABLE redaction_audit_findings (
    id TEXT PRIMARY KEY,
    capture_id TEXT NOT NULL REFERENCES captures(id),
    workspace_id TEXT NOT NULL REFERENCES workspaces(id),
    applied_ruleset_version INTEGER,
    evaluation_ruleset_version INTEGER NOT NULL REFERENCES redaction_rulesets(version),
    rule_ids JSONB NOT NULL DEFAULT '[]'::jsonb,
    event_ids JSONB NOT NULL DEFAULT '[]'::jsonb,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
-- +goose StatementEnd

-- Append-only, same defence-in-depth as audit_log/capture_events/mutations/
-- comments/redaction_rulesets in 00002_schema.sql: a finding is a factual
-- record of what a specific audit run detected and must never be edited
-- or removed after the fact.
-- +goose StatementBegin
CREATE TRIGGER redaction_audit_findings_no_update BEFORE UPDATE OR DELETE ON redaction_audit_findings
    FOR EACH ROW EXECUTE FUNCTION reject_mutation();
-- +goose StatementEnd

-- No explicit GRANT needed: 00002_schema.sql's
-- `ALTER DEFAULT PRIVILEGES IN SCHEMA public GRANT SELECT, INSERT ON TABLES
-- TO htr_runtime` already covers new tables created after it, giving
-- htr_runtime SELECT+INSERT and, crucially, no UPDATE/DELETE.

-- +goose StatementBegin
CREATE INDEX redaction_audit_findings_capture_id_idx ON redaction_audit_findings(capture_id);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS redaction_audit_findings CASCADE;
-- +goose StatementEnd
