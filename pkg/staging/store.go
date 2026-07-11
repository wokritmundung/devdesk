// Package staging implements the node-local snapshot store: the "local NVMe"
// tier from the one-pager's SLA table. Layout under the root:
//
//	<root>/<snapshot-id>/manifest.json
//	<root>/<snapshot-id>/...payload files owned by the checkpointer...
//
// Manifests are written atomically (tmp + rename). A snapshot without a
// Complete manifest never existed as far as restore is concerned.
package staging

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"time"

	"github.com/wokritmundung/devdesk/pkg/api"
)

var ErrNotFound = errors.New("snapshot not found")

// Store is a snapshot store rooted at a local directory.
type Store struct {
	root string
}

func NewStore(root string) (*Store, error) {
	if err := os.MkdirAll(root, 0o750); err != nil {
		return nil, fmt.Errorf("staging root: %w", err)
	}
	return &Store{root: root}, nil
}

// Begin allocates a snapshot directory and writes an incomplete manifest.
// The checkpointer writes payload into the returned dir, then calls Commit.
func (s *Store) Begin(podUID string, mode api.Mode) (api.SnapshotManifest, error) {
	id, err := newID()
	if err != nil {
		return api.SnapshotManifest{}, err
	}
	m := api.SnapshotManifest{
		ID:        id,
		PodUID:    podUID,
		Mode:      mode,
		CreatedAt: time.Now().UTC(),
		Dir:       filepath.Join(s.root, id),
		Complete:  false,
	}
	if err := os.MkdirAll(m.Dir, 0o750); err != nil {
		return api.SnapshotManifest{}, err
	}
	if err := s.writeManifest(m); err != nil {
		return api.SnapshotManifest{}, err
	}
	return m, nil
}

// Commit finalizes a snapshot: records payload size, marks Complete, and
// persists the manifest atomically.
func (s *Store) Commit(m api.SnapshotManifest) (api.SnapshotManifest, error) {
	size, err := dirSize(m.Dir)
	if err != nil {
		return m, err
	}
	m.SizeBytes = size
	m.Complete = true
	return m, s.writeManifest(m)
}

// Abort removes an in-flight snapshot.
func (s *Store) Abort(m api.SnapshotManifest) error {
	return os.RemoveAll(m.Dir)
}

// Get returns a snapshot manifest by ID.
func (s *Store) Get(id string) (api.SnapshotManifest, error) {
	var m api.SnapshotManifest
	b, err := os.ReadFile(filepath.Join(s.root, id, "manifest.json"))
	if errors.Is(err, os.ErrNotExist) {
		return m, ErrNotFound
	}
	if err != nil {
		return m, err
	}
	return m, json.Unmarshal(b, &m)
}

// List returns all Complete snapshots, newest first.
func (s *Store) List() ([]api.SnapshotManifest, error) {
	entries, err := os.ReadDir(s.root)
	if err != nil {
		return nil, err
	}
	var out []api.SnapshotManifest
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		m, err := s.Get(e.Name())
		if err != nil || !m.Complete {
			continue
		}
		out = append(out, m)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt.After(out[j].CreatedAt) })
	return out, nil
}

// Sweep removes incomplete snapshots (crashed checkpoints). Run at startup.
func (s *Store) Sweep() (int, error) {
	entries, err := os.ReadDir(s.root)
	if err != nil {
		return 0, err
	}
	n := 0
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		m, err := s.Get(e.Name())
		if err != nil || !m.Complete {
			if rmErr := os.RemoveAll(filepath.Join(s.root, e.Name())); rmErr == nil {
				n++
			}
		}
	}
	return n, nil
}

func (s *Store) writeManifest(m api.SnapshotManifest) error {
	b, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return err
	}
	tmp := filepath.Join(m.Dir, ".manifest.tmp")
	if err := os.WriteFile(tmp, b, 0o640); err != nil {
		return err
	}
	return os.Rename(tmp, filepath.Join(m.Dir, "manifest.json"))
}

func dirSize(dir string) (int64, error) {
	var size int64
	err := filepath.Walk(dir, func(_ string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if !info.IsDir() {
			size += info.Size()
		}
		return nil
	})
	return size, err
}

func newID() (string, error) {
	b := make([]byte, 8)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return time.Now().UTC().Format("20060102t150405") + "-" + hex.EncodeToString(b), nil
}
