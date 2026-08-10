-- +goose Up
-- +goose StatementBegin
CREATE TABLE workspaces (
    id TEXT PRIMARY KEY,
    name TEXT NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TABLE projects (
    id TEXT PRIMARY KEY,
    workspace_id TEXT NOT NULL REFERENCES workspaces(id),
    name TEXT NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TABLE users (
    id TEXT PRIMARY KEY,
    email TEXT NOT NULL UNIQUE,
    name TEXT NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TABLE memberships (
    id TEXT PRIMARY KEY,
    workspace_id TEXT NOT NULL REFERENCES workspaces(id),
    user_id TEXT NOT NULL REFERENCES users(id),
    role TEXT NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (workspace_id, user_id)
);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TABLE redaction_rulesets (
    version INTEGER PRIMARY KEY,
    workspace_id TEXT NOT NULL REFERENCES workspaces(id),
    rules JSONB NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TABLE captures (
    id TEXT PRIMARY KEY,
    workspace_id TEXT NOT NULL REFERENCES workspaces(id),
    project_id TEXT NOT NULL REFERENCES projects(id),
    source TEXT NOT NULL,
    state TEXT NOT NULL,
    fidelity TEXT NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    epoch TIMESTAMPTZ NOT NULL,
    env JSONB NOT NULL DEFAULT '{}'::jsonb,
    metadata JSONB NOT NULL DEFAULT '{}'::jsonb,
    doc JSONB NOT NULL DEFAULT '{}'::jsonb,
    withheld_event_count INTEGER NOT NULL DEFAULT 0,
    revision BIGINT NOT NULL DEFAULT 0,
    manifest_complete BOOLEAN NOT NULL DEFAULT false,
    applied_ruleset_version INTEGER REFERENCES redaction_rulesets(version)
);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TABLE capture_events (
    id TEXT PRIMARY KEY,
    capture_id TEXT NOT NULL REFERENCES captures(id),
    t BIGINT NOT NULL,
    kind TEXT NOT NULL,
    payload JSONB NOT NULL DEFAULT '{}'::jsonb,
    redaction JSONB NOT NULL DEFAULT '{}'::jsonb,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TABLE assets (
    id TEXT PRIMARY KEY,
    capture_id TEXT NOT NULL REFERENCES captures(id),
    kind TEXT NOT NULL,
    mime_type TEXT NOT NULL,
    size_bytes BIGINT NOT NULL DEFAULT 0,
    chunk_count INTEGER NOT NULL DEFAULT 0,
    sha256 TEXT,
    object_key TEXT NOT NULL,
    verified_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TABLE mutations (
    id TEXT PRIMARY KEY,
    capture_id TEXT NOT NULL REFERENCES captures(id),
    op TEXT NOT NULL,
    payload JSONB NOT NULL DEFAULT '{}'::jsonb,
    client_t BIGINT NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TABLE comments (
    id TEXT PRIMARY KEY,
    capture_id TEXT NOT NULL REFERENCES captures(id),
    author_id TEXT NOT NULL REFERENCES users(id),
    body TEXT NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TABLE audit_log (
    id TEXT PRIMARY KEY,
    workspace_id TEXT NOT NULL REFERENCES workspaces(id),
    actor_id TEXT,
    action TEXT NOT NULL,
    subject TEXT NOT NULL,
    details JSONB NOT NULL DEFAULT '{}'::jsonb,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TABLE share_links (
    id TEXT PRIMARY KEY,
    capture_id TEXT NOT NULL REFERENCES captures(id),
    token TEXT NOT NULL UNIQUE,
    created_by TEXT NOT NULL REFERENCES users(id),
    revoked_at TIMESTAMPTZ,
    expires_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TABLE integration_bindings (
    id TEXT PRIMARY KEY,
    workspace_id TEXT NOT NULL REFERENCES workspaces(id),
    provider TEXT NOT NULL,
    config JSONB NOT NULL DEFAULT '{}'::jsonb,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TABLE outbox (
    id TEXT PRIMARY KEY,
    topic TEXT NOT NULL,
    payload JSONB NOT NULL DEFAULT '{}'::jsonb,
    dispatched_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
-- +goose StatementEnd

-- Immutability: append-only tables reject UPDATE and DELETE via trigger,
-- as defence in depth on top of the role grants below (which are what
-- actually stop the runtime role; the trigger also catches the owner).
-- +goose StatementBegin
CREATE FUNCTION reject_mutation() RETURNS TRIGGER AS $$
BEGIN
    RAISE EXCEPTION '% is append-only: % not permitted', TG_TABLE_NAME, TG_OP;
END;
$$ LANGUAGE plpgsql;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER capture_events_no_update BEFORE UPDATE OR DELETE ON capture_events
    FOR EACH ROW EXECUTE FUNCTION reject_mutation();
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER audit_log_no_update BEFORE UPDATE OR DELETE ON audit_log
    FOR EACH ROW EXECUTE FUNCTION reject_mutation();
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER mutations_no_update BEFORE UPDATE OR DELETE ON mutations
    FOR EACH ROW EXECUTE FUNCTION reject_mutation();
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER comments_no_update BEFORE UPDATE OR DELETE ON comments
    FOR EACH ROW EXECUTE FUNCTION reject_mutation();
-- +goose StatementEnd

-- redaction_rulesets is versioned and replaced wholesale, never edited:
-- applied_ruleset_version and audit attribution depend on a ruleset
-- version's rules never changing after creation, so it gets the same
-- append-only trigger as the event/log tables above.
-- +goose StatementBegin
CREATE TRIGGER redaction_rulesets_no_update BEFORE UPDATE OR DELETE ON redaction_rulesets
    FOR EACH ROW EXECUTE FUNCTION reject_mutation();
-- +goose StatementEnd

-- Two roles: migration role owns the schema (the connecting/superuser role
-- that ran this migration already owns it); htr_runtime is what every
-- service connects as. It gets SELECT+INSERT everywhere, but never
-- UPDATE/DELETE on the append-only tables, and gets full CRUD only on
-- tables genuinely mutated in place (workspaces/projects/users/memberships/
-- captures/assets/redaction_rulesets/share_links/integration_bindings/
-- outbox dispatch bookkeeping).
-- +goose StatementBegin
DO $$
BEGIN
    IF NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'htr_runtime') THEN
        CREATE ROLE htr_runtime LOGIN PASSWORD 'htr_runtime';
    END IF;
END
$$;
-- +goose StatementEnd

-- +goose StatementBegin
GRANT USAGE ON SCHEMA public TO htr_runtime;
-- +goose StatementEnd

-- +goose StatementBegin
GRANT SELECT, INSERT, UPDATE, DELETE ON
    workspaces, projects, users, memberships,
    captures, assets, share_links,
    integration_bindings, outbox
    TO htr_runtime;
-- +goose StatementEnd

-- +goose StatementBegin
GRANT SELECT, INSERT ON capture_events, mutations, comments, audit_log, redaction_rulesets TO htr_runtime;
-- +goose StatementEnd

-- +goose StatementBegin
REVOKE UPDATE, DELETE ON capture_events, mutations, comments, audit_log, redaction_rulesets FROM htr_runtime;
-- +goose StatementEnd

-- +goose StatementBegin
ALTER DEFAULT PRIVILEGES IN SCHEMA public GRANT SELECT, INSERT ON TABLES TO htr_runtime;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP OWNED BY htr_runtime;
-- +goose StatementEnd

-- +goose StatementBegin
DROP TABLE IF EXISTS outbox, integration_bindings, share_links, audit_log,
    comments, mutations, assets, capture_events, captures,
    redaction_rulesets, memberships, users, projects, workspaces CASCADE;
-- +goose StatementEnd

-- +goose StatementBegin
DROP FUNCTION IF EXISTS reject_mutation();
-- +goose StatementEnd

-- +goose StatementBegin
ALTER DEFAULT PRIVILEGES IN SCHEMA public REVOKE SELECT, INSERT ON TABLES FROM htr_runtime;
-- +goose StatementEnd

-- +goose StatementBegin
DROP ROLE IF EXISTS htr_runtime;
-- +goose StatementEnd
