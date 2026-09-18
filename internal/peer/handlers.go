package peer

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
)

// requestError carries the HTTP status a request-decoding failure should
// produce, distinguishing an oversized body (413) from any other decode
// failure (400).
type requestError struct {
	status int
	err    error
}

func (e *requestError) Error() string { return e.err.Error() }
func (e *requestError) Unwrap() error { return e.err }

// decodeBody reads r's body, capped at maxBodyBytes, and JSON-decodes it
// into v.
func decodeBody(w http.ResponseWriter, r *http.Request, v any) *requestError {
	r.Body = http.MaxBytesReader(w, r.Body, maxBodyBytes)
	if err := json.NewDecoder(r.Body).Decode(v); err != nil {
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			return &requestError{status: http.StatusRequestEntityTooLarge, err: fmt.Errorf("peer: request body exceeds %d bytes: %w", maxBodyBytes, err)}
		}
		return &requestError{status: http.StatusBadRequest, err: fmt.Errorf("peer: decode request body: %w", err)}
	}
	return nil
}

// writeJSON writes v as the JSON response body with the given status.
func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v) // headers are already sent; nothing to recover
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
		writeError(w, rerr.status, "bad request")
		return
	}
	id, err := s.h.ReceiveReport(r.Context(), env)
	if err != nil {
		s.logger.Error("peer: receive report failed", "err", err)
		writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	writeJSON(w, http.StatusOK, Ack{Status: "ok", ID: id})
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
	writeJSON(w, http.StatusOK, env)
}

// handleDecision implements POST /v1/decision: record the decision.
// Phase 3 is observe-only (ADR-006): nothing acts on it until Phase 4.
func (s *server) handleDecision(w http.ResponseWriter, r *http.Request) {
	var d Decision
	if rerr := decodeBody(w, r, &d); rerr != nil {
		writeError(w, rerr.status, "bad request")
		return
	}
	id, err := s.h.ReceiveDecision(r.Context(), d)
	if err != nil {
		s.logger.Error("peer: receive decision failed", "err", err)
		writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	writeJSON(w, http.StatusAccepted, Ack{Status: "accepted", ID: id})
}

// handleHeartbeat implements POST /v1/heartbeat: record this node is alive.
func (s *server) handleHeartbeat(w http.ResponseWriter, r *http.Request) {
	var hb Heartbeat
	if rerr := decodeBody(w, r, &hb); rerr != nil {
		writeError(w, rerr.status, "bad request")
		return
	}
	if err := s.h.ReceiveHeartbeat(r.Context(), hb); err != nil {
		s.logger.Error("peer: receive heartbeat failed", "err", err)
		writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	writeJSON(w, http.StatusOK, Ack{Status: "ok"})
}
