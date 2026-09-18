package peer

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
)

// requestError carries the outcome of a failed request decode: status and
// msg are safe to send to the client as-is, while err (the full detail)
// belongs only in slog.
type requestError struct {
	status int
	msg    string
	err    error
}

// decodeBody reads r's body, capped at maxBodyBytes, and JSON-decodes it
// into v.
func decodeBody(w http.ResponseWriter, r *http.Request, v any) *requestError {
	r.Body = http.MaxBytesReader(w, r.Body, maxBodyBytes)
	if err := json.NewDecoder(r.Body).Decode(v); err != nil {
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			return &requestError{
				status: http.StatusRequestEntityTooLarge,
				msg:    "request body too large",
				err:    fmt.Errorf("peer: request body exceeds %d bytes: %w", maxBodyBytes, err),
			}
		}
		return &requestError{
			status: http.StatusBadRequest,
			msg:    "bad request",
			err:    fmt.Errorf("peer: decode request body: %w", err),
		}
	}
	return nil
}

// rejectRequest logs the decode failure's detail (never sent to the
// client) and writes the safe status/message pair as the response. It
// logs at Warn: a 400/413 on this peer-to-peer channel is either a bug in
// the calling node or a hostile request, not routine/expected traffic.
func (s *server) rejectRequest(w http.ResponseWriter, r *http.Request, rerr *requestError) {
	s.logger.Warn("peer: rejecting request", "method", r.Method, "path", r.URL.Path, "status", rerr.status, "err", rerr.err)
	http.Error(w, rerr.msg, rerr.status)
}

// writeJSON writes v as the JSON response body with the given status. A
// failure here (the client disconnecting mid-write, a broken pipe, ...)
// happens after headers are already sent, so there is nothing left to
// recover; it is logged rather than silently dropped. r's body is never
// logged, only its method/path.
func (s *server) writeJSON(w http.ResponseWriter, r *http.Request, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(v); err != nil {
		s.logger.Warn("peer: write response failed", "method", r.Method, "path", r.URL.Path, "err", err)
	}
}

// writeError writes a generic, detail-free error body. The real error
// (when there is one) belongs only in slog, never in the response.
func writeError(w http.ResponseWriter, status int, msg string) {
	http.Error(w, msg, status)
}

// handleReport implements POST /v1/report: store env as the peer's report.
func (s *server) handleReport(w http.ResponseWriter, r *http.Request) {
	var env ReportEnvelope
	if rerr := decodeBody(w, r, &env); rerr != nil {
		s.rejectRequest(w, r, rerr)
		return
	}
	id, err := s.h.ReceiveReport(r.Context(), env)
	if err != nil {
		s.logger.Error("peer: receive report failed", "err", err)
		writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	s.writeJSON(w, r, http.StatusOK, Ack{Status: "ok", ID: id})
}

// handleLatestReport implements GET /v1/report/latest: return this node's
// own most recent report, or 404 when it has none yet.
func (s *server) handleLatestReport(w http.ResponseWriter, r *http.Request) {
	env, ok, err := s.h.LatestOwnReport(r.Context())
	if err != nil {
		s.logger.Error("peer: latest own report failed", "err", err)
		writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	if !ok {
		writeError(w, http.StatusNotFound, "not found")
		return
	}
	s.writeJSON(w, r, http.StatusOK, env)
}

// handleDecision implements POST /v1/decision: record the decision.
// Phase 3 is observe-only (ADR-006): nothing acts on it until Phase 4.
func (s *server) handleDecision(w http.ResponseWriter, r *http.Request) {
	var d Decision
	if rerr := decodeBody(w, r, &d); rerr != nil {
		s.rejectRequest(w, r, rerr)
		return
	}
	id, err := s.h.ReceiveDecision(r.Context(), d)
	if err != nil {
		s.logger.Error("peer: receive decision failed", "err", err)
		writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	s.writeJSON(w, r, http.StatusAccepted, Ack{Status: "accepted", ID: id})
}

// handleHeartbeat implements POST /v1/heartbeat: record this node is alive.
func (s *server) handleHeartbeat(w http.ResponseWriter, r *http.Request) {
	var hb Heartbeat
	if rerr := decodeBody(w, r, &hb); rerr != nil {
		s.rejectRequest(w, r, rerr)
		return
	}
	if err := s.h.ReceiveHeartbeat(r.Context(), hb); err != nil {
		s.logger.Error("peer: receive heartbeat failed", "err", err)
		writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	s.writeJSON(w, r, http.StatusOK, Ack{Status: "ok"})
}
