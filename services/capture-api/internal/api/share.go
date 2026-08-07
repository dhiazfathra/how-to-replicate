package api

import (
	"errors"
	"net"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/dhiazfathra/how-to-replicate/services/capture-api/internal/sharelink"
	"github.com/dhiazfathra/how-to-replicate/services/capture-api/internal/store"
)

// shareHandler serves the single unauthenticated share-link resolution
// route. It is the one endpoint in capture-api not gated by
// auth.Middleware — see NewShareRouter.
type shareHandler struct {
	store   *store.Store
	signer  sharelink.Signer
	limiter *sharelink.Limiter
}

// resolve is THE enforcement point for a share link: signature, key
// validity, and expiry are checked by signer.Verify; revocation and
// capture state are checked here against the live rows (never trusted
// from the token itself, since either can change after the token was
// issued). Every successful resolution writes exactly one audit_log entry.
// Content is returned only through gateCapture, the same single gate
// getCapture/listCaptures/listEvents route through — so a share link can
// never disclose more than an authenticated read of the same capture
// would.
func (h *shareHandler) resolve(w http.ResponseWriter, r *http.Request) {
	if !h.limiter.Allow(clientKey(r), time.Now()) {
		http.Error(w, "too many requests", http.StatusTooManyRequests)
		return
	}

	token := chi.URLParam(r, "token")
	shareLinkID, captureID, err := h.signer.Verify(token, time.Now())
	if err != nil {
		if errors.Is(err, sharelink.ErrExpired) {
			http.Error(w, "expired", http.StatusGone)
			return
		}
		http.Error(w, "invalid token", http.StatusNotFound)
		return
	}

	sl, err := h.store.GetShareLink(r.Context(), shareLinkID)
	if err != nil || sl.CaptureID != captureID || sl.RevokedAt.Valid {
		http.Error(w, "invalid token", http.StatusNotFound)
		return
	}

	c, err := h.store.GetCaptureByID(r.Context(), captureID)
	if err != nil {
		http.Error(w, "invalid token", http.StatusNotFound)
		return
	}
	if c.State != captureReadyState {
		http.Error(w, "not ready", http.StatusNotFound)
		return
	}

	if _, err := h.store.CreateAuditLog(r.Context(), newID(), c.WorkspaceID, "",
		"share_link.resolve", captureID, []byte(`{"shareLinkId":"`+shareLinkID+`"}`)); err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	writeJSON(w, http.StatusOK, gateCapture(c))
}

// clientKey extracts the rate-limit key: the client's IP, stripped of port.
// Falls back to the raw RemoteAddr if it isn't a host:port pair (e.g. in
// some test transports).
func clientKey(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}
