package staging

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/wokritmundung/devdesk/pkg/api"
)

func TestBeginCommitLifecycle(t *testing.T) {
	s, err := NewStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	m, err := s.Begin("pod-1", api.ModeEngineNative)
	if err != nil {
		t.Fatal(err)
	}
	// Incomplete snapshots are invisible to List.
	if list, _ := s.List(); len(list) != 0 {
		t.Fatalf("incomplete snapshot leaked into List: %+v", list)
	}
	if err := os.WriteFile(filepath.Join(m.Dir, "payload.bin"), make([]byte, 1024), 0o640); err != nil {
		t.Fatal(err)
	}
	m, err = s.Commit(m)
	if err != nil {
		t.Fatal(err)
	}
	if !m.Complete || m.SizeBytes < 1024 {
		t.Fatalf("commit didn't finalize: %+v", m)
	}
	got, err := s.Get(m.ID)
	if err != nil || got.ID != m.ID {
		t.Fatalf("get after commit: %v %+v", err, got)
	}
}

func TestSweepRemovesIncomplete(t *testing.T) {
	root := t.TempDir()
	s, _ := NewStore(root)
	m1, _ := s.Begin("pod-1", api.ModeEngineNative) // left incomplete
	m2, _ := s.Begin("pod-2", api.ModeEngineNative)
	os.WriteFile(filepath.Join(m2.Dir, "x"), []byte("x"), 0o640)
	if _, err := s.Commit(m2); err != nil {
		t.Fatal(err)
	}
	n, err := s.Sweep()
	if err != nil || n != 1 {
		t.Fatalf("sweep: n=%d err=%v", n, err)
	}
	if _, err := os.Stat(m1.Dir); !os.IsNotExist(err) {
		t.Fatal("incomplete snapshot survived sweep")
	}
	if list, _ := s.List(); len(list) != 1 {
		t.Fatal("complete snapshot should survive sweep")
	}
}
