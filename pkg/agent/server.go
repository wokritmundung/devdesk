// Package agent implements the node-agent API server: the M1 (JSON/HTTP)
// form of the contract that becomes gRPC in M2. Endpoints:
//
//	POST /v1/checkpoint  {api.CheckpointRequest}  -> {api.CheckpointResponse}
//	POST /v1/restore     {api.RestoreRequest}     -> {api.RestoreResponse}
//	GET  /v1/snapshots                            -> [{api.SnapshotManifest}]
//	GET  /healthz
package agent

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"time"

	"github.com/wokritmundung/devdesk/pkg/api"
	"github.com/wokritmundung/devdesk/pkg/checkpoint"
	"github.com/wokritmundung/devdesk/pkg/staging"
)

// Server routes agent API calls to the staging store and checkpoint paths.
type Server struct {
	Store *staging.Store
	// Paths maps mode -> implementation. Missing mode => 501.
	Paths map[api.Mode]checkpoint.Checkpointer
	Log   *slog.Logger
}

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /v1/checkpoint", s.handleCheckpoint)
	mux.HandleFunc("POST /v1/restore", s.handleRestore)
	mux.HandleFunc("GET /v1/snapshots", s.handleList)
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	return mux
}

func (s *Server) handleCheckpoint(w http.ResponseWriter, r *http.Request) {
	var req api.CheckpointRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, "bad_request", err.Error())
		return
	}
	if req.PodUID == "" {
		writeErr(w, http.StatusBadRequest, "bad_request", "podUID is required")
		return
	}
	cp, ok := s.Paths[req.Mode]
	if !ok {
		writeErr(w, http.StatusNotImplemented, "mode_unsupported", string(req.Mode))
		return
	}

	ctx := r.Context()
	var cancel context.CancelFunc
	if req.DeadlineSeconds > 0 {
		ctx, cancel = context.WithTimeout(ctx, time.Duration(req.DeadlineSeconds)*time.Second)
		defer cancel()
	}

	start := time.Now()
	m, err := s.Store.Begin(req.PodUID, req.Mode)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "staging_error", err.Error())
		return
	}
	if err := cp.Checkpoint(ctx, req, m.Dir); err != nil {
		_ = s.Store.Abort(m)
		status, code := classify(err)
		writeErr(w, status, code, err.Error())
		return
	}
	m, err = s.Store.Commit(m)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "staging_error", err.Error())
		return
	}
	s.log().Info("checkpoint complete", "snapshot", m.ID, "pod", req.PodUID,
		"mode", req.Mode, "bytes", m.SizeBytes, "elapsed", time.Since(start))
	writeJSON(w, http.StatusOK, api.CheckpointResponse{
		Snapshot:  m,
		ElapsedMS: time.Since(start).Milliseconds(),
	})
}

func (s *Server) handleRestore(w http.ResponseWriter, r *http.Request) {
	var req api.RestoreRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, "bad_request", err.Error())
		return
	}
	m, err := s.Store.Get(req.SnapshotID)
	if errors.Is(err, staging.ErrNotFound) {
		writeErr(w, http.StatusNotFound, "not_found", req.SnapshotID)
		return
	}
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "staging_error", err.Error())
		return
	}
	if !m.Complete {
		writeErr(w, http.StatusConflict, "snapshot_incomplete", m.ID)
		return
	}
	cp, ok := s.Paths[m.Mode]
	if !ok {
		writeErr(w, http.StatusNotImplemented, "mode_unsupported", string(m.Mode))
		return
	}
	start := time.Now()
	if err := cp.Restore(r.Context(), req.EngineURL, m.Dir); err != nil {
		status, code := classify(err)
		writeErr(w, status, code, err.Error())
		return
	}
	s.log().Info("restore complete", "snapshot", m.ID, "elapsed", time.Since(start))
	writeJSON(w, http.StatusOK, api.RestoreResponse{
		Snapshot:  m,
		ElapsedMS: time.Since(start).Milliseconds(),
	})
}

func (s *Server) handleList(w http.ResponseWriter, _ *http.Request) {
	list, err := s.Store.List()
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "staging_error", err.Error())
		return
	}
	if list == nil {
		list = []api.SnapshotManifest{}
	}
	writeJSON(w, http.StatusOK, list)
}

func (s *Server) log() *slog.Logger {
	if s.Log != nil {
		return s.Log
	}
	return slog.Default()
}

func classify(err error) (int, string) {
	if errors.Is(err, checkpoint.ErrUnavailable) {
		return http.StatusNotImplemented, "path_unavailable"
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return http.StatusGatewayTimeout, "deadline_exceeded"
	}
	return http.StatusBadGateway, "checkpoint_failed"
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeErr(w http.ResponseWriter, status int, code, msg string) {
	writeJSON(w, status, api.Error{Code: code, Message: msg})
}
