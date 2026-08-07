package api

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgtype"

	"github.com/dhiazfathra/how-to-replicate/services/capture-api/internal/sharelink"
	"github.com/dhiazfathra/how-to-replicate/services/capture-api/internal/store"
	"github.com/dhiazfathra/how-to-replicate/services/internal/db/sqlcgen"
)

var errBoom = errors.New("boom")

func testHandlersWithSigner(fq fakeQuerier, signer sharelink.Signer) *handlers {
	return &handlers{store: store.New(fq), signer: signer}
}

// nonReadyStates enumerates every state gateCapture must collapse down to
// status-metadata-only, per invariant 1 and the task-11 brief.
var nonReadyStates = []string{"recording", "redacting", "composing", "failed", "expired"}

func readyCapture(state string) sqlcgen.Capture {
	return sqlcgen.Capture{
		ID:                 "cap_1",
		WorkspaceID:        "ws_1",
		ProjectID:          "proj_1",
		State:              state,
		Fidelity:           "full",
		CreatedAt:          pgtype.Timestamptz{Time: time.Unix(1000, 0), Valid: true},
		Doc:                []byte(`{"steps":["a"]}`),
		Metadata:           []byte(`{"browser":"chrome"}`),
		Env:                []byte(`{"os":"mac"}`),
		WithheldEventCount: 3,
	}
}

func assertStatusOnly(t *testing.T, v CaptureView, c sqlcgen.Capture) {
	t.Helper()
	if v.ID != c.ID || v.State != c.State || v.Fidelity != c.Fidelity {
		t.Fatalf("status fields mismatch: %+v vs %+v", v, c)
	}
	if v.CreatedAt == "" {
		t.Fatalf("expected non-empty createdAt")
	}
	if v.Doc != nil {
		t.Fatalf("doc leaked for non-ready capture: %s", v.Doc)
	}
	if v.Metadata != nil {
		t.Fatalf("metadata leaked for non-ready capture: %s", v.Metadata)
	}
	if v.Env != nil {
		t.Fatalf("env leaked for non-ready capture: %s", v.Env)
	}
	if v.WithheldEventCount != 0 {
		t.Fatalf("withheldEventCount leaked for non-ready capture: %d", v.WithheldEventCount)
	}
	if v.Events != nil {
		t.Fatalf("events leaked for non-ready capture: %+v", v.Events)
	}
}

func TestGetCapture_GatesEveryNonReadyState(t *testing.T) {
	for _, state := range nonReadyStates {
		t.Run(state, func(t *testing.T) {
			c := readyCapture(state)
			h := newTestHandlers(fakeQuerier{capture: c, captureOK: true})

			r := httptest.NewRequest(http.MethodGet, "/v1/workspaces/ws_1/captures/cap_1", nil)
			r = withChiParams(r, map[string]string{"workspaceID": "ws_1", "captureID": "cap_1"})
			rec := httptest.NewRecorder()

			h.getCapture(rec, r)

			if rec.Code != http.StatusOK {
				t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
			}
			var got CaptureView
			if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
				t.Fatalf("decode: %v", err)
			}
			assertStatusOnly(t, got, c)

			// Also assert at the raw-bytes level: no doc/metadata/env/events
			// key should appear at all, since they're omitempty.
			for _, key := range []string{`"doc"`, `"metadata"`, `"env"`, `"events"`, `"withheldEventCount"`} {
				if strings.Contains(rec.Body.String(), key) {
					t.Fatalf("raw response contains %s for non-ready capture: %s", key, rec.Body.String())
				}
			}
		})
	}
}

func TestGetCapture_ReadyReturnsFullContent(t *testing.T) {
	c := readyCapture(captureReadyState)
	h := newTestHandlers(fakeQuerier{capture: c, captureOK: true})

	r := httptest.NewRequest(http.MethodGet, "/v1/workspaces/ws_1/captures/cap_1", nil)
	r = withChiParams(r, map[string]string{"workspaceID": "ws_1", "captureID": "cap_1"})
	rec := httptest.NewRecorder()

	h.getCapture(rec, r)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var got CaptureView
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if string(got.Doc) != `{"steps":["a"]}` {
		t.Fatalf("doc missing for ready capture: %+v", got)
	}
	if string(got.Metadata) != `{"browser":"chrome"}` {
		t.Fatalf("metadata missing for ready capture: %+v", got)
	}
	if got.WithheldEventCount != 3 {
		t.Fatalf("withheldEventCount = %d, want 3", got.WithheldEventCount)
	}
}

func TestGetCapture_NotFound(t *testing.T) {
	h := newTestHandlers(fakeQuerier{captureOK: false})
	r := httptest.NewRequest(http.MethodGet, "/v1/workspaces/ws_1/captures/cap_1", nil)
	r = withChiParams(r, map[string]string{"workspaceID": "ws_1", "captureID": "cap_1"})
	rec := httptest.NewRecorder()

	h.getCapture(rec, r)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", rec.Code)
	}
}

func TestListCaptures_GatesEveryNonReadyState(t *testing.T) {
	for _, state := range nonReadyStates {
		t.Run(state, func(t *testing.T) {
			c := readyCapture(state)
			h := newTestHandlers(fakeQuerier{capture: c, captureOK: true})

			r := httptest.NewRequest(http.MethodGet, "/v1/workspaces/ws_1/captures", nil)
			r = withChiParams(r, map[string]string{"workspaceID": "ws_1"})
			rec := httptest.NewRecorder()

			h.listCaptures(rec, r)

			if rec.Code != http.StatusOK {
				t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
			}
			var got []CaptureView
			if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
				t.Fatalf("decode: %v", err)
			}
			if len(got) != 1 {
				t.Fatalf("len = %d, want 1", len(got))
			}
			assertStatusOnly(t, got[0], c)
		})
	}
}

func TestListCaptures_ReadyReturnsFullContent(t *testing.T) {
	c := readyCapture(captureReadyState)
	h := newTestHandlers(fakeQuerier{capture: c, captureOK: true})

	r := httptest.NewRequest(http.MethodGet, "/v1/workspaces/ws_1/captures", nil)
	r = withChiParams(r, map[string]string{"workspaceID": "ws_1"})
	rec := httptest.NewRecorder()

	h.listCaptures(rec, r)

	var got []CaptureView
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(got) != 1 || string(got[0].Doc) != `{"steps":["a"]}` {
		t.Fatalf("unexpected body: %+v", got)
	}
}

func TestListEvents_GatesEveryNonReadyState(t *testing.T) {
	events := []sqlcgen.CaptureEvent{{ID: "ev_1", CaptureID: "cap_1", T: 1, Kind: "click", Payload: []byte(`{"x":1}`)}}
	for _, state := range nonReadyStates {
		t.Run(state, func(t *testing.T) {
			c := readyCapture(state)
			h := newTestHandlers(fakeQuerier{capture: c, captureOK: true, captures: events})

			r := httptest.NewRequest(http.MethodGet, "/v1/workspaces/ws_1/captures/cap_1/events", nil)
			r = withChiParams(r, map[string]string{"workspaceID": "ws_1", "captureID": "cap_1"})
			rec := httptest.NewRecorder()

			h.listEvents(rec, r)

			if rec.Code != http.StatusOK {
				t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
			}
			var got []CaptureEventView
			if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
				t.Fatalf("decode: %v", err)
			}
			if len(got) != 0 {
				t.Fatalf("events leaked for non-ready capture: %+v", got)
			}
			if rec.Body.String() != "null\n" && strings.TrimSpace(rec.Body.String()) != "[]" {
				t.Fatalf("expected empty events body, got %s", rec.Body.String())
			}
		})
	}
}

func TestListEvents_ReadyReturnsFullContent(t *testing.T) {
	c := readyCapture(captureReadyState)
	events := []sqlcgen.CaptureEvent{{ID: "ev_1", CaptureID: "cap_1", T: 1, Kind: "click", Payload: []byte(`{"x":1}`)}}
	h := newTestHandlers(fakeQuerier{capture: c, captureOK: true, captures: events})

	r := httptest.NewRequest(http.MethodGet, "/v1/workspaces/ws_1/captures/cap_1/events", nil)
	r = withChiParams(r, map[string]string{"workspaceID": "ws_1", "captureID": "cap_1"})
	rec := httptest.NewRecorder()

	h.listEvents(rec, r)

	var got []CaptureEventView
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(got) != 1 || got[0].ID != "ev_1" {
		t.Fatalf("unexpected body: %+v", got)
	}
}

func TestListCaptures_StoreError(t *testing.T) {
	h := newTestHandlers(fakeQuerier{err: errBoom})
	r := httptest.NewRequest(http.MethodGet, "/v1/workspaces/ws_1/captures", nil)
	r = withChiParams(r, map[string]string{"workspaceID": "ws_1"})
	rec := httptest.NewRecorder()

	h.listCaptures(rec, r)

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", rec.Code)
	}
}

func TestListEvents_QueryError(t *testing.T) {
	c := readyCapture(captureReadyState)
	h := newTestHandlers(fakeQuerier{capture: c, captureOK: true, eventsErr: errBoom})
	r := httptest.NewRequest(http.MethodGet, "/v1/workspaces/ws_1/captures/cap_1/events", nil)
	r = withChiParams(r, map[string]string{"workspaceID": "ws_1", "captureID": "cap_1"})
	rec := httptest.NewRecorder()

	h.listEvents(rec, r)

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", rec.Code)
	}
}

func TestCreateShareLink_StoreError(t *testing.T) {
	c := readyCapture(captureReadyState)
	signer := sharelink.Signer{Current: sharelink.Key{ID: "k1", Secret: []byte("secret")}}
	h := testHandlersWithSigner(fakeQuerier{capture: c, captureOK: true, err: errBoom}, signer)

	r := httptest.NewRequest(http.MethodPost, "/v1/workspaces/ws_1/captures/cap_1/share-links", strings.NewReader(`{"expiresInSeconds":60}`))
	r = withSubject(r, "user-1")
	r = withChiParams(r, map[string]string{"workspaceID": "ws_1", "captureID": "cap_1"})
	rec := httptest.NewRecorder()

	h.createShareLink(rec, r)

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", rec.Code)
	}
}

func TestNewShareRouter_Builds(t *testing.T) {
	signer := sharelink.Signer{Current: sharelink.Key{ID: "k1", Secret: []byte("secret")}}
	NewShareRouter(store.New(fakeQuerier{}), signer, &sharelink.Limiter{Max: 10, Window: time.Minute})
}

func TestListEvents_CaptureNotFound(t *testing.T) {
	h := newTestHandlers(fakeQuerier{captureOK: false})
	r := httptest.NewRequest(http.MethodGet, "/v1/workspaces/ws_1/captures/cap_1/events", nil)
	r = withChiParams(r, map[string]string{"workspaceID": "ws_1", "captureID": "cap_1"})
	rec := httptest.NewRecorder()

	h.listEvents(rec, r)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", rec.Code)
	}
}

func TestCreateComment_Success(t *testing.T) {
	c := readyCapture(captureReadyState)
	h := newTestHandlers(fakeQuerier{capture: c, captureOK: true, comment: sqlcgen.Comment{ID: "c_1", CaptureID: "cap_1", Body: "hi"}})

	r := httptest.NewRequest(http.MethodPost, "/v1/workspaces/ws_1/captures/cap_1/comments", strings.NewReader(`{"body":"hi"}`))
	r = withSubject(r, "user-1")
	r = withChiParams(r, map[string]string{"workspaceID": "ws_1", "captureID": "cap_1"})
	rec := httptest.NewRecorder()

	h.createComment(rec, r)

	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
}

func TestCreateComment_Unauthenticated(t *testing.T) {
	h := newTestHandlers(fakeQuerier{})
	r := httptest.NewRequest(http.MethodPost, "/v1/workspaces/ws_1/captures/cap_1/comments", strings.NewReader(`{"body":"hi"}`))
	r = withChiParams(r, map[string]string{"workspaceID": "ws_1", "captureID": "cap_1"})
	rec := httptest.NewRecorder()

	h.createComment(rec, r)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", rec.Code)
	}
}

func TestCreateComment_InvalidBody(t *testing.T) {
	h := newTestHandlers(fakeQuerier{})
	r := httptest.NewRequest(http.MethodPost, "/v1/workspaces/ws_1/captures/cap_1/comments", strings.NewReader(`{}`))
	r = withSubject(r, "user-1")
	r = withChiParams(r, map[string]string{"workspaceID": "ws_1", "captureID": "cap_1"})
	rec := httptest.NewRecorder()

	h.createComment(rec, r)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
}

// TestCreateComment_WorkspaceScoping_Rejected exercises the workspace-scoping
// requirement directly against the store: a capture that belongs to a
// different workspace than the one in the URL must be rejected, exactly
// like it doesn't exist.
func TestCreateComment_WorkspaceScoping_Rejected(t *testing.T) {
	c := readyCapture(captureReadyState)
	c.WorkspaceID = "ws_other"
	h := newTestHandlers(fakeQuerier{capture: c, captureOK: false}) // GetCaptureForWorkspace(id, "ws_1") finds nothing

	r := httptest.NewRequest(http.MethodPost, "/v1/workspaces/ws_1/captures/cap_1/comments", strings.NewReader(`{"body":"hi"}`))
	r = withSubject(r, "user-1")
	r = withChiParams(r, map[string]string{"workspaceID": "ws_1", "captureID": "cap_1"})
	rec := httptest.NewRecorder()

	h.createComment(rec, r)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404 (workspace-scoped rejection)", rec.Code)
	}
}

func TestCreateShareLink_Success(t *testing.T) {
	c := readyCapture(captureReadyState)
	signer := sharelink.Signer{Current: sharelink.Key{ID: "k1", Secret: []byte("secret")}}
	h := testHandlersWithSigner(fakeQuerier{capture: c, captureOK: true, shareLink: sqlcgen.ShareLink{ID: "sl_1", CaptureID: "cap_1"}}, signer)

	r := httptest.NewRequest(http.MethodPost, "/v1/workspaces/ws_1/captures/cap_1/share-links", strings.NewReader(`{"expiresInSeconds":3600}`))
	r = withSubject(r, "user-1")
	r = withChiParams(r, map[string]string{"workspaceID": "ws_1", "captureID": "cap_1"})
	rec := httptest.NewRecorder()

	h.createShareLink(rec, r)

	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var resp shareLinkResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil || resp.Token == "" {
		t.Fatalf("unexpected body: %s (err=%v)", rec.Body.String(), err)
	}
}

func TestCreateShareLink_InvalidExpiry(t *testing.T) {
	h := newTestHandlers(fakeQuerier{})
	r := httptest.NewRequest(http.MethodPost, "/v1/workspaces/ws_1/captures/cap_1/share-links", strings.NewReader(`{"expiresInSeconds":0}`))
	r = withSubject(r, "user-1")
	r = withChiParams(r, map[string]string{"workspaceID": "ws_1", "captureID": "cap_1"})
	rec := httptest.NewRecorder()

	h.createShareLink(rec, r)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
}

func TestCreateShareLink_Unauthenticated(t *testing.T) {
	h := newTestHandlers(fakeQuerier{})
	r := httptest.NewRequest(http.MethodPost, "/v1/workspaces/ws_1/captures/cap_1/share-links", strings.NewReader(`{"expiresInSeconds":60}`))
	r = withChiParams(r, map[string]string{"workspaceID": "ws_1", "captureID": "cap_1"})
	rec := httptest.NewRecorder()

	h.createShareLink(rec, r)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", rec.Code)
	}
}

func TestCreateShareLink_CaptureNotInWorkspace(t *testing.T) {
	h := newTestHandlers(fakeQuerier{captureOK: false})
	r := httptest.NewRequest(http.MethodPost, "/v1/workspaces/ws_1/captures/cap_1/share-links", strings.NewReader(`{"expiresInSeconds":60}`))
	r = withSubject(r, "user-1")
	r = withChiParams(r, map[string]string{"workspaceID": "ws_1", "captureID": "cap_1"})
	rec := httptest.NewRecorder()

	h.createShareLink(rec, r)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", rec.Code)
	}
}

func TestRevokeShareLink(t *testing.T) {
	c := readyCapture(captureReadyState)
	sl := sqlcgen.ShareLink{ID: "sl_1", CaptureID: "cap_1"}
	h := newTestHandlers(fakeQuerier{capture: c, captureOK: true, shareLink: sl, shareLinkOK: true})

	r := httptest.NewRequest(http.MethodPost, "/v1/workspaces/ws_1/captures/cap_1/share-links/sl_1/revoke", nil)
	r = withChiParams(r, map[string]string{"workspaceID": "ws_1", "captureID": "cap_1", "shareLinkID": "sl_1"})
	rec := httptest.NewRecorder()

	h.revokeShareLink(rec, r)

	if rec.Code != http.StatusNoContent {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
}

func TestRevokeShareLink_NotFound(t *testing.T) {
	h := newTestHandlers(fakeQuerier{shareLinkOK: false})
	r := httptest.NewRequest(http.MethodPost, "/v1/workspaces/ws_1/captures/cap_1/share-links/sl_1/revoke", nil)
	r = withChiParams(r, map[string]string{"workspaceID": "ws_1", "captureID": "cap_1", "shareLinkID": "sl_1"})
	rec := httptest.NewRecorder()

	h.revokeShareLink(rec, r)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", rec.Code)
	}
}

// --- share-link resolver ---

func newResolveRequest(token string) *http.Request {
	r := httptest.NewRequest(http.MethodGet, "/v1/share/"+token, nil)
	r.RemoteAddr = "203.0.113.1:5555"
	return withChiParams(r, map[string]string{"token": token})
}

func TestResolveShareLink_Success_WritesExactlyOneAuditEntry(t *testing.T) {
	c := readyCapture(captureReadyState)
	signer := sharelink.Signer{Current: sharelink.Key{ID: "k1", Secret: []byte("secret")}}
	token, err := signer.Sign("sl_1", "cap_1", time.Now().Add(time.Hour))
	if err != nil {
		t.Fatalf("sign: %v", err)
	}

	count := 0
	fq := fakeQuerier{
		capture:       c,
		captureOK:     true,
		shareLink:     sqlcgen.ShareLink{ID: "sl_1", CaptureID: "cap_1"},
		shareLinkOK:   true,
		auditLogCount: &count,
	}
	h := &shareHandler{store: store.New(fq), signer: signer, limiter: &sharelink.Limiter{Max: 100, Window: time.Minute}}

	rec := httptest.NewRecorder()
	h.resolve(rec, newResolveRequest(token))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	if count != 1 {
		t.Fatalf("audit log count = %d, want exactly 1", count)
	}
	var got CaptureView
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil || string(got.Doc) == "" {
		t.Fatalf("expected full content in resolved capture: %s (err=%v)", rec.Body.String(), err)
	}
}

func TestResolveShareLink_Expired(t *testing.T) {
	signer := sharelink.Signer{Current: sharelink.Key{ID: "k1", Secret: []byte("secret")}}
	token, _ := signer.Sign("sl_1", "cap_1", time.Now().Add(-time.Hour))

	h := &shareHandler{store: store.New(fakeQuerier{}), signer: signer, limiter: &sharelink.Limiter{Max: 100, Window: time.Minute}}
	rec := httptest.NewRecorder()
	h.resolve(rec, newResolveRequest(token))

	if rec.Code != http.StatusGone {
		t.Fatalf("status = %d, want 410", rec.Code)
	}
}

func TestResolveShareLink_Revoked(t *testing.T) {
	c := readyCapture(captureReadyState)
	signer := sharelink.Signer{Current: sharelink.Key{ID: "k1", Secret: []byte("secret")}}
	token, _ := signer.Sign("sl_1", "cap_1", time.Now().Add(time.Hour))

	fq := fakeQuerier{
		capture:     c,
		captureOK:   true,
		shareLink:   sqlcgen.ShareLink{ID: "sl_1", CaptureID: "cap_1", RevokedAt: pgtype.Timestamptz{Time: time.Now(), Valid: true}},
		shareLinkOK: true,
	}
	h := &shareHandler{store: store.New(fq), signer: signer, limiter: &sharelink.Limiter{Max: 100, Window: time.Minute}}
	rec := httptest.NewRecorder()
	h.resolve(rec, newResolveRequest(token))

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404 for revoked token", rec.Code)
	}
}

func TestResolveShareLink_NonReadyCaptureState(t *testing.T) {
	for _, state := range nonReadyStates {
		t.Run(state, func(t *testing.T) {
			c := readyCapture(state)
			signer := sharelink.Signer{Current: sharelink.Key{ID: "k1", Secret: []byte("secret")}}
			token, _ := signer.Sign("sl_1", "cap_1", time.Now().Add(time.Hour))

			fq := fakeQuerier{
				capture:     c,
				captureOK:   true,
				shareLink:   sqlcgen.ShareLink{ID: "sl_1", CaptureID: "cap_1"},
				shareLinkOK: true,
			}
			h := &shareHandler{store: store.New(fq), signer: signer, limiter: &sharelink.Limiter{Max: 100, Window: time.Minute}}
			rec := httptest.NewRecorder()
			h.resolve(rec, newResolveRequest(token))

			if rec.Code != http.StatusNotFound {
				t.Fatalf("status = %d, want 404 for non-ready capture state %s", rec.Code, state)
			}
		})
	}
}

func TestResolveShareLink_ForgedSignature(t *testing.T) {
	signerA := sharelink.Signer{Current: sharelink.Key{ID: "k1", Secret: []byte("secret-a")}}
	signerB := sharelink.Signer{Current: sharelink.Key{ID: "k1", Secret: []byte("secret-b")}}
	token, _ := signerA.Sign("sl_1", "cap_1", time.Now().Add(time.Hour))

	// signerB doesn't know secret-a, so it must reject a token minted by
	// signerA even though the key ID matches.
	h := &shareHandler{store: store.New(fakeQuerier{}), signer: signerB, limiter: &sharelink.Limiter{Max: 100, Window: time.Minute}}
	rec := httptest.NewRecorder()
	h.resolve(rec, newResolveRequest(token))

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404 for forged signature", rec.Code)
	}
}

func TestResolveShareLink_MalformedToken(t *testing.T) {
	signer := sharelink.Signer{Current: sharelink.Key{ID: "k1", Secret: []byte("secret")}}
	h := &shareHandler{store: store.New(fakeQuerier{}), signer: signer, limiter: &sharelink.Limiter{Max: 100, Window: time.Minute}}
	rec := httptest.NewRecorder()
	h.resolve(rec, newResolveRequest("not-a-real-token"))

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404 for malformed token", rec.Code)
	}
}

func TestResolveShareLink_ShareLinkCaptureMismatch(t *testing.T) {
	c := readyCapture(captureReadyState)
	signer := sharelink.Signer{Current: sharelink.Key{ID: "k1", Secret: []byte("secret")}}
	token, _ := signer.Sign("sl_1", "cap_1", time.Now().Add(time.Hour))

	// The stored share link's capture_id doesn't match what the token
	// claims — must be rejected even though the signature verifies.
	fq := fakeQuerier{
		capture:     c,
		captureOK:   true,
		shareLink:   sqlcgen.ShareLink{ID: "sl_1", CaptureID: "cap_other"},
		shareLinkOK: true,
	}
	h := &shareHandler{store: store.New(fq), signer: signer, limiter: &sharelink.Limiter{Max: 100, Window: time.Minute}}
	rec := httptest.NewRecorder()
	h.resolve(rec, newResolveRequest(token))

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404 for capture id mismatch", rec.Code)
	}
}

func TestResolveShareLink_CaptureRowMissing(t *testing.T) {
	signer := sharelink.Signer{Current: sharelink.Key{ID: "k1", Secret: []byte("secret")}}
	token, _ := signer.Sign("sl_1", "cap_1", time.Now().Add(time.Hour))

	// Share link row exists and matches, but the capture it points at is
	// gone (captureOK false leaves GetCaptureByID returning ErrNotFound).
	fq := fakeQuerier{
		shareLink:   sqlcgen.ShareLink{ID: "sl_1", CaptureID: "cap_1"},
		shareLinkOK: true,
		captureOK:   false,
	}
	h := &shareHandler{store: store.New(fq), signer: signer, limiter: &sharelink.Limiter{Max: 100, Window: time.Minute}}
	rec := httptest.NewRecorder()
	h.resolve(rec, newResolveRequest(token))

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", rec.Code)
	}
}

func TestResolveShareLink_AuditLogWriteFails(t *testing.T) {
	c := readyCapture(captureReadyState)
	signer := sharelink.Signer{Current: sharelink.Key{ID: "k1", Secret: []byte("secret")}}
	token, _ := signer.Sign("sl_1", "cap_1", time.Now().Add(time.Hour))

	fq := fakeQuerier{
		capture:     c,
		captureOK:   true,
		shareLink:   sqlcgen.ShareLink{ID: "sl_1", CaptureID: "cap_1"},
		shareLinkOK: true,
		auditErr:    errBoom,
	}
	h := &shareHandler{store: store.New(fq), signer: signer, limiter: &sharelink.Limiter{Max: 100, Window: time.Minute}}
	rec := httptest.NewRecorder()
	h.resolve(rec, newResolveRequest(token))

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", rec.Code)
	}
}

func TestResolveShareLink_ShareLinkRowMissing(t *testing.T) {
	signer := sharelink.Signer{Current: sharelink.Key{ID: "k1", Secret: []byte("secret")}}
	token, _ := signer.Sign("sl_1", "cap_1", time.Now().Add(time.Hour))

	h := &shareHandler{store: store.New(fakeQuerier{shareLinkOK: false}), signer: signer, limiter: &sharelink.Limiter{Max: 100, Window: time.Minute}}
	rec := httptest.NewRecorder()
	h.resolve(rec, newResolveRequest(token))

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", rec.Code)
	}
}

func TestResolveShareLink_RateLimited(t *testing.T) {
	signer := sharelink.Signer{Current: sharelink.Key{ID: "k1", Secret: []byte("secret")}}
	limiter := &sharelink.Limiter{Max: 2, Window: time.Minute}
	h := &shareHandler{store: store.New(fakeQuerier{}), signer: signer, limiter: limiter}

	var codes []int
	for i := 0; i < 3; i++ {
		rec := httptest.NewRecorder()
		h.resolve(rec, newResolveRequest("whatever"))
		codes = append(codes, rec.Code)
	}
	if codes[2] != http.StatusTooManyRequests {
		t.Fatalf("codes = %v, want 3rd request rate-limited (429)", codes)
	}
}

func TestClientKey(t *testing.T) {
	r := httptest.NewRequest(http.MethodGet, "/", nil)
	r.RemoteAddr = "10.0.0.5:1234"
	if got := clientKey(r); got != "10.0.0.5" {
		t.Fatalf("clientKey = %q, want 10.0.0.5", got)
	}

	r2 := httptest.NewRequest(http.MethodGet, "/", nil)
	r2.RemoteAddr = "not-a-host-port"
	if got := clientKey(r2); got != "not-a-host-port" {
		t.Fatalf("clientKey fallback = %q, want raw RemoteAddr", got)
	}
}
