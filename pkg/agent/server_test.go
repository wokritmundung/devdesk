package agent

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/wokritmundung/devdesk/pkg/api"
	"github.com/wokritmundung/devdesk/pkg/checkpoint"
	"github.com/wokritmundung/devdesk/pkg/staging"
)

// fakeVLLM emulates the slice of the vLLM dev API the engine-native path
// uses: /sleep, /wake_up, /is_sleeping, /v1/models.
type fakeVLLM struct {
	mu       sync.Mutex
	sleeping bool
	sleeps   int
	wakes    int
}

func (f *fakeVLLM) handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /sleep", func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		f.sleeping, f.sleeps = true, f.sleeps+1
		f.mu.Unlock()
		w.WriteHeader(http.StatusOK)
	})
	mux.HandleFunc("POST /wake_up", func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		f.sleeping, f.wakes = false, f.wakes+1
		f.mu.Unlock()
		w.WriteHeader(http.StatusOK)
	})
	mux.HandleFunc("GET /is_sleeping", func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		defer f.mu.Unlock()
		json.NewEncoder(w).Encode(map[string]bool{"is_sleeping": f.sleeping})
	})
	mux.HandleFunc("GET /v1/models", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"data":[{"id":"meta-llama/Llama-3.1-70B"}]}`))
	})
	return mux
}

func newTestServer(t *testing.T, engineURL string) *Server {
	t.Helper()
	store, err := staging.NewStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return &Server{
		Store: store,
		Paths: map[api.Mode]checkpoint.Checkpointer{
			api.ModeEngineNative: &checkpoint.EngineNative{DefaultEngineURL: engineURL},
			api.ModeProcess:      &checkpoint.Process{CudaCheckpointBin: "/nonexistent", CriuBin: "/nonexistent"},
		},
	}
}

func doJSON(t *testing.T, h http.Handler, method, path string, body any) *httptest.ResponseRecorder {
	t.Helper()
	var buf bytes.Buffer
	if body != nil {
		if err := json.NewEncoder(&buf).Encode(body); err != nil {
			t.Fatal(err)
		}
	}
	req := httptest.NewRequest(method, path, &buf)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	return rr
}

func TestCheckpointRestoreRoundTrip(t *testing.T) {
	engine := &fakeVLLM{}
	es := httptest.NewServer(engine.handler())
	defer es.Close()

	srv := newTestServer(t, es.URL)
	h := srv.Handler()

	// Checkpoint quiesces the engine and produces a complete snapshot.
	rr := doJSON(t, h, "POST", "/v1/checkpoint", api.CheckpointRequest{
		PodUID: "pod-abc", Mode: api.ModeEngineNative,
	})
	if rr.Code != http.StatusOK {
		t.Fatalf("checkpoint: got %d: %s", rr.Code, rr.Body)
	}
	var cresp api.CheckpointResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &cresp); err != nil {
		t.Fatal(err)
	}
	if !cresp.Snapshot.Complete || cresp.Snapshot.SizeBytes == 0 {
		t.Fatalf("snapshot not complete or empty: %+v", cresp.Snapshot)
	}
	if engine.sleeps != 1 || !engine.sleeping {
		t.Fatalf("engine should be sleeping after checkpoint (sleeps=%d)", engine.sleeps)
	}

	// List sees exactly the one complete snapshot.
	rr = doJSON(t, h, "GET", "/v1/snapshots", nil)
	var list []api.SnapshotManifest
	if err := json.Unmarshal(rr.Body.Bytes(), &list); err != nil {
		t.Fatal(err)
	}
	if len(list) != 1 || list[0].ID != cresp.Snapshot.ID {
		t.Fatalf("list mismatch: %+v", list)
	}

	// Restore wakes the engine.
	rr = doJSON(t, h, "POST", "/v1/restore", api.RestoreRequest{SnapshotID: cresp.Snapshot.ID})
	if rr.Code != http.StatusOK {
		t.Fatalf("restore: got %d: %s", rr.Code, rr.Body)
	}
	if engine.wakes != 1 || engine.sleeping {
		t.Fatalf("engine should be awake after restore (wakes=%d)", engine.wakes)
	}
}

func TestCheckpointFailureAbortsSnapshot(t *testing.T) {
	// Engine that refuses to sleep.
	es := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "nope", http.StatusInternalServerError)
	}))
	defer es.Close()

	srv := newTestServer(t, es.URL)
	h := srv.Handler()

	rr := doJSON(t, h, "POST", "/v1/checkpoint", api.CheckpointRequest{
		PodUID: "pod-abc", Mode: api.ModeEngineNative,
	})
	if rr.Code != http.StatusBadGateway {
		t.Fatalf("want 502, got %d: %s", rr.Code, rr.Body)
	}
	// Failed checkpoints must not leave restore candidates behind.
	rr = doJSON(t, h, "GET", "/v1/snapshots", nil)
	if body := strings.TrimSpace(rr.Body.String()); body != "[]" {
		t.Fatalf("expected empty snapshot list, got %s", body)
	}
}

func TestProcessModeUnavailableIsHonest(t *testing.T) {
	srv := newTestServer(t, "http://unused")
	h := srv.Handler()
	rr := doJSON(t, h, "POST", "/v1/checkpoint", api.CheckpointRequest{
		PodUID: "pod-abc", Mode: api.ModeProcess, ProcessPID: 4242,
	})
	if rr.Code != http.StatusNotImplemented {
		t.Fatalf("want 501 path_unavailable, got %d: %s", rr.Code, rr.Body)
	}
	var e api.Error
	if err := json.Unmarshal(rr.Body.Bytes(), &e); err != nil || e.Code != "path_unavailable" {
		t.Fatalf("want path_unavailable envelope, got %s", rr.Body)
	}
}

func TestRestoreUnknownSnapshotIs404(t *testing.T) {
	srv := newTestServer(t, "http://unused")
	rr := doJSON(t, srv.Handler(), "POST", "/v1/restore", api.RestoreRequest{SnapshotID: "nope"})
	if rr.Code != http.StatusNotFound {
		t.Fatalf("want 404, got %d", rr.Code)
	}
}
