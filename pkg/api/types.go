// Package api defines the wire types for the rewarm node-agent API.
//
// M1 transport is JSON over HTTP; the types are kept flat and
// protobuf-friendly so the M2 gRPC contract is a mechanical translation.
package api

import "time"

// Mode selects one of the two checkpoint paths. The two-path split is
// load-bearing (see docs/architecture.md, Design B): EngineNative is the
// light/partial intra-cluster path, Process is the heavy/total node-death path.
type Mode string

const (
	// ModeEngineNative cooperates with the inference engine (vLLM
	// sleep/wake + state export). Light, partial, fast to restore locally.
	ModeEngineNative Mode = "engine-native"
	// ModeProcess checkpoints the whole engine process via
	// cuda-checkpoint + CRIU. Heavy, total, survives node death once
	// streamed to a remote tier (M3).
	ModeProcess Mode = "process"
)

// CheckpointRequest asks the node agent to checkpoint one workload.
type CheckpointRequest struct {
	// PodUID identifies the workload pod. In M1 (manual trigger) this is
	// an opaque label; in M2 the preemption hook supplies the real UID.
	PodUID string `json:"podUID"`
	// Mode selects the checkpoint path.
	Mode Mode `json:"mode"`
	// DeadlineSeconds bounds the whole operation. For spot reclamation
	// this is derived from the cloud warning window minus escape budget.
	// Zero means no deadline (manual/testing).
	DeadlineSeconds int `json:"deadlineSeconds,omitempty"`
	// EngineURL overrides the agent's default engine endpoint (tests,
	// multi-engine nodes). Optional.
	EngineURL string `json:"engineURL,omitempty"`
	// ProcessPID is the engine PID for ModeProcess. Optional in M1.
	ProcessPID int `json:"processPID,omitempty"`
}

// SnapshotManifest describes one snapshot at rest in the staging store.
// The manifest is the unit of truth: restore, GC, and (M3) remote tiering
// all operate on manifests, never on raw paths.
type SnapshotManifest struct {
	ID          string    `json:"id"`
	PodUID      string    `json:"podUID"`
	Mode        Mode      `json:"mode"`
	CreatedAt   time.Time `json:"createdAt"`
	SizeBytes   int64     `json:"sizeBytes"`
	// Dir is the snapshot's directory inside the staging root.
	Dir string `json:"dir"`
	// EngineVersion pins engine compatibility for restore. Best-effort in M1.
	EngineVersion string `json:"engineVersion,omitempty"`
	// Complete is false while a checkpoint is in flight; incomplete
	// snapshots are never restore candidates and are GC'd on startup.
	Complete bool `json:"complete"`
}

// CheckpointResponse reports the outcome of a checkpoint request.
type CheckpointResponse struct {
	Snapshot SnapshotManifest `json:"snapshot"`
	// ElapsedMS is wall time for the whole operation; the number we
	// benchmark against the SLA table.
	ElapsedMS int64 `json:"elapsedMS"`
}

// RestoreRequest asks the agent to restore a snapshot to a target engine.
type RestoreRequest struct {
	SnapshotID string `json:"snapshotID"`
	// EngineURL is the engine that should receive the restored state.
	EngineURL string `json:"engineURL,omitempty"`
}

// RestoreResponse reports restore outcome.
type RestoreResponse struct {
	Snapshot  SnapshotManifest `json:"snapshot"`
	ElapsedMS int64            `json:"elapsedMS"`
}

// Error is the uniform error envelope for the agent API.
type Error struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}
