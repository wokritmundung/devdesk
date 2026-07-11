// Package checkpoint implements the two checkpoint paths of Design B.
// Both paths satisfy Checkpointer; the agent selects by api.Mode. Keep the
// paths separate (CLAUDE.md convention) — they have different SLAs, failure
// modes, and maturity, and merging them would blur the two-SLA contract.
package checkpoint

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/wokritmundung/devdesk/pkg/api"
)

// ErrUnavailable means this path cannot run on this node (missing binaries,
// engine doesn't support the API, etc.). The agent reports it as such rather
// than failing generically, so policy can fall back (e.g. recompute).
var ErrUnavailable = errors.New("checkpoint path unavailable")

// Checkpointer is the narrow seam described in docs/architecture.md: the
// same interface an unprivileged Design-A sidecar could implement later.
type Checkpointer interface {
	// Checkpoint captures state for the workload into dir (inside a
	// staging.Store snapshot directory).
	Checkpoint(ctx context.Context, req api.CheckpointRequest, dir string) error
	// Restore pushes state from dir back into a running engine.
	Restore(ctx context.Context, engineURL, dir string) error
	// Mode reports which path this implements.
	Mode() api.Mode
}

// ---------------------------------------------------------------------------
// Engine-native path: cooperate with vLLM over its HTTP API.
//
// Sequence (M1, against vLLM's dev/sleep API):
//  1. POST {engine}/sleep?level=1   — engine offloads weights, discards or
//     offloads KV per level; serving pauses.
//  2. GET  {engine}/is_sleeping     — confirm quiesced.
//  3. Persist an engine state descriptor to the snapshot dir. In M1 this is
//     the engine's reported state (version, model, sleep level); KV block
//     export lands here when the vLLM connector API stabilizes (M2).
//  4. Restore: POST {engine}/wake_up, verify awake.
//
// Honest scope note: level-1 sleep is closer to serialize-and-free than a
// graceful pause; that is exactly the M1 finding we need to benchmark.
// ---------------------------------------------------------------------------

// EngineNative implements Checkpointer against a vLLM-compatible engine API.
type EngineNative struct {
	// DefaultEngineURL is used when the request doesn't override it.
	DefaultEngineURL string
	// HTTPClient allows test injection; nil means a sane default.
	HTTPClient *http.Client
}

func (e *EngineNative) Mode() api.Mode { return api.ModeEngineNative }

func (e *EngineNative) client() *http.Client {
	if e.HTTPClient != nil {
		return e.HTTPClient
	}
	return &http.Client{Timeout: 30 * time.Second}
}

func (e *EngineNative) engineURL(req api.CheckpointRequest) (string, error) {
	u := req.EngineURL
	if u == "" {
		u = e.DefaultEngineURL
	}
	if u == "" {
		return "", fmt.Errorf("%w: no engine URL configured", ErrUnavailable)
	}
	if _, err := url.Parse(u); err != nil {
		return "", fmt.Errorf("bad engine URL: %w", err)
	}
	return strings.TrimRight(u, "/"), nil
}

// stateDescriptor is what M1 persists for the engine-native path.
type stateDescriptor struct {
	EngineURL    string    `json:"engineURL"`
	SleepLevel   int       `json:"sleepLevel"`
	CapturedAt   time.Time `json:"capturedAt"`
	EngineInfo   string    `json:"engineInfo,omitempty"`
	ModelName    string    `json:"modelName,omitempty"`
	KVExported   bool      `json:"kvExported"` // false in M1; true once block export ships
}

func (e *EngineNative) Checkpoint(ctx context.Context, req api.CheckpointRequest, dir string) error {
	base, err := e.engineURL(req)
	if err != nil {
		return err
	}

	// 1. Quiesce: put the engine to sleep.
	if err := e.post(ctx, base+"/sleep?level=1"); err != nil {
		return fmt.Errorf("engine sleep: %w", err)
	}

	// 2. Confirm quiesced.
	sleeping, err := e.getJSON(ctx, base+"/is_sleeping")
	if err != nil {
		return fmt.Errorf("engine is_sleeping: %w", err)
	}

	// 3. Capture best-effort engine identity for restore compatibility.
	desc := stateDescriptor{
		EngineURL:  base,
		SleepLevel: 1,
		CapturedAt: time.Now().UTC(),
		EngineInfo: strings.TrimSpace(sleeping),
	}
	if models, err := e.getJSON(ctx, base+"/v1/models"); err == nil {
		desc.ModelName = firstModelID(models)
	}

	b, err := json.MarshalIndent(desc, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, "engine-state.json"), b, 0o640)
}

func (e *EngineNative) Restore(ctx context.Context, engineURL, dir string) error {
	b, err := os.ReadFile(filepath.Join(dir, "engine-state.json"))
	if err != nil {
		return fmt.Errorf("snapshot missing engine state: %w", err)
	}
	var desc stateDescriptor
	if err := json.Unmarshal(b, &desc); err != nil {
		return err
	}
	base := strings.TrimRight(engineURL, "/")
	if base == "" {
		base = desc.EngineURL
	}
	if base == "" {
		return fmt.Errorf("%w: no engine URL for restore", ErrUnavailable)
	}
	if err := e.post(ctx, base+"/wake_up"); err != nil {
		return fmt.Errorf("engine wake_up: %w", err)
	}
	return nil
}

func (e *EngineNative) post(ctx context.Context, u string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, u, nil)
	if err != nil {
		return err
	}
	resp, err := e.client().Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return fmt.Errorf("engine returned %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	return nil
}

func (e *EngineNative) getJSON(ctx context.Context, u string) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return "", err
	}
	resp, err := e.client().Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return "", err
	}
	if resp.StatusCode >= 300 {
		return "", fmt.Errorf("engine returned %d", resp.StatusCode)
	}
	return string(body), nil
}

func firstModelID(modelsJSON string) string {
	var v struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if err := json.Unmarshal([]byte(modelsJSON), &v); err != nil || len(v.Data) == 0 {
		return ""
	}
	return v.Data[0].ID
}
