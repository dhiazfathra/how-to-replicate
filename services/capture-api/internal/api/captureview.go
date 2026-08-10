package api

import (
	"encoding/json"

	"github.com/jackc/pgx/v5/pgtype"

	"github.com/dhiazfathra/how-to-replicate/services/internal/db/sqlcgen"
)

// captureReadyState is the only state in which a capture's content is
// disclosable (invariant 1 in CLAUDE.md: "no unredacted capture is
// viewable, exportable, or routable. The gate is state === 'ready'").
const captureReadyState = "ready"

// CaptureView is the response shape for every read path that can return a
// capture: the authenticated GetCapture/ListCaptures/ListEvents handlers
// and the (unauthenticated) share-link resolver. For any capture not in
// state "ready" it carries status metadata only — no Doc, no Events, no
// Metadata — regardless of what's actually in the row.
//
// Every field uses omitempty plus a zero value from gateCapture for
// non-ready captures, so there's no bytes-on-the-wire path (e.g. an empty
// JSON object `{}` vs a populated one) that would let a client distinguish
// "no doc" from "doc withheld" — both serialize identically to absent.
type CaptureView struct {
	ID        string `json:"id"`
	State     string `json:"state"`
	Fidelity  string `json:"fidelity"`
	CreatedAt string `json:"createdAt"`

	Doc                json.RawMessage    `json:"doc,omitempty"`
	Metadata           json.RawMessage    `json:"metadata,omitempty"`
	Env                json.RawMessage    `json:"env,omitempty"`
	WithheldEventCount int32              `json:"withheldEventCount,omitempty"`
	Events             []CaptureEventView `json:"events,omitempty"`
}

// CaptureEventView is the wire shape of a single capture event. It is only
// ever populated by ListEvents for a ready capture; gateCapture never
// attaches events to a non-ready CaptureView.
type CaptureEventView struct {
	ID      string          `json:"id"`
	T       int64           `json:"t"`
	Kind    string          `json:"kind"`
	Payload json.RawMessage `json:"payload"`
}

// gateCapture is the single place that enforces invariant 1. Every read
// path that can expose a capture — GetCapture, ListCaptures, ListEvents,
// and the share-link resolver — must build its response through this
// function (or gateCaptureWithEvents below, which delegates to it) rather
// than serializing a sqlcgen.Capture directly, so a future read path
// cannot forget the gate.
func gateCapture(c sqlcgen.Capture) CaptureView {
	v := CaptureView{
		ID:        c.ID,
		State:     c.State,
		Fidelity:  c.Fidelity,
		CreatedAt: timestamptzRFC3339(c.CreatedAt),
	}
	if c.State != captureReadyState {
		return v
	}
	v.Doc = json.RawMessage(orNullJSON(c.Doc))
	v.Metadata = json.RawMessage(orNullJSON(c.Metadata))
	v.Env = json.RawMessage(orNullJSON(c.Env))
	v.WithheldEventCount = c.WithheldEventCount
	return v
}

// gateCaptureEvents returns evs as CaptureEventViews if c is ready, or nil
// otherwise — so ListEvents can never return event content for a
// non-ready capture even if the caller forgets to check gateCapture's
// output first.
func gateCaptureEvents(c sqlcgen.Capture, evs []sqlcgen.CaptureEvent) []CaptureEventView {
	if c.State != captureReadyState {
		return nil
	}
	out := make([]CaptureEventView, 0, len(evs))
	for _, e := range evs {
		out = append(out, CaptureEventView{
			ID:      e.ID,
			T:       e.T,
			Kind:    e.Kind,
			Payload: json.RawMessage(orNullJSON(e.Payload)),
		})
	}
	return out
}

func orNullJSON(b []byte) []byte {
	if len(b) == 0 {
		return []byte("null")
	}
	return b
}

func timestamptzRFC3339(t pgtype.Timestamptz) string {
	if !t.Valid {
		return ""
	}
	return t.Time.UTC().Format("2006-01-02T15:04:05.000Z07:00")
}
