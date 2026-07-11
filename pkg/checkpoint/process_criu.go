package checkpoint

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"

	"github.com/wokritmundung/devdesk/pkg/api"
)

// Process implements the heavy/total path: freeze GPU state with
// cuda-checkpoint, then dump the whole engine process with CRIU. This is the
// node-death path — the resulting snapshot is self-contained and (in M3)
// streamable to a remote tier.
//
// Maturity note (one-pager, Key risks): cuda-checkpoint/CRIU-GPU are young.
// This implementation probes for the binaries and returns ErrUnavailable
// rather than pretending; real-GPU validation is manual in M1 (CLAUDE.md
// conventions) and this code path is exercised in CI only via LookPath
// guards and argument-construction tests.
type Process struct {
	// CudaCheckpointBin and CriuBin allow overriding binary locations
	// (and injecting fakes in tests). Empty means look up in PATH.
	CudaCheckpointBin string
	CriuBin           string
}

func (p *Process) Mode() api.Mode { return api.ModeProcess }

func (p *Process) bins() (cuda string, criu string, err error) {
	resolve := func(explicit, name string) (string, error) {
		if explicit != "" {
			if _, err := os.Stat(explicit); err != nil {
				return "", fmt.Errorf("%w: %s not found at %s", ErrUnavailable, name, explicit)
			}
			return explicit, nil
		}
		path, err := exec.LookPath(name)
		if err != nil {
			return "", fmt.Errorf("%w: %s not found in PATH", ErrUnavailable, name)
		}
		return path, nil
	}
	if cuda, err = resolve(p.CudaCheckpointBin, "cuda-checkpoint"); err != nil {
		return "", "", err
	}
	if criu, err = resolve(p.CriuBin, "criu"); err != nil {
		return "", "", err
	}
	return cuda, criu, nil
}

func (p *Process) Checkpoint(ctx context.Context, req api.CheckpointRequest, dir string) error {
	if req.ProcessPID <= 0 {
		return fmt.Errorf("%w: process mode requires processPID", ErrUnavailable)
	}
	cuda, criu, err := p.bins()
	if err != nil {
		return err
	}
	pid := strconv.Itoa(req.ProcessPID)
	imgDir := filepath.Join(dir, "criu")
	if err := os.MkdirAll(imgDir, 0o750); err != nil {
		return err
	}

	// 1. Toggle CUDA state out of the GPU into host memory so CRIU can
	//    see a checkpointable process.
	if out, err := exec.CommandContext(ctx, cuda, "--toggle", "--pid", pid).CombinedOutput(); err != nil {
		return fmt.Errorf("cuda-checkpoint toggle (suspend): %v: %s", err, out)
	}

	// 2. CRIU dump. --leave-running keeps the process alive on failure
	//    paths in M1 experiments; eviction is the scheduler's job, not ours.
	args := []string{
		"dump", "--tree", pid, "--images-dir", imgDir,
		"--shell-job", "--tcp-established", "--leave-running",
	}
	if out, err := exec.CommandContext(ctx, criu, args...).CombinedOutput(); err != nil {
		// Best effort: toggle CUDA state back so the workload isn't
		// left suspended by a failed dump.
		_, _ = exec.CommandContext(ctx, cuda, "--toggle", "--pid", pid).CombinedOutput()
		return fmt.Errorf("criu dump: %v: %s", err, out)
	}

	// 3. Resume GPU work in the still-running original.
	if out, err := exec.CommandContext(ctx, cuda, "--toggle", "--pid", pid).CombinedOutput(); err != nil {
		return fmt.Errorf("cuda-checkpoint toggle (resume): %v: %s", err, out)
	}
	return nil
}

func (p *Process) Restore(ctx context.Context, _ string, dir string) error {
	_, criu, err := p.bins()
	if err != nil {
		return err
	}
	imgDir := filepath.Join(dir, "criu")
	if _, err := os.Stat(imgDir); err != nil {
		return fmt.Errorf("snapshot has no criu image dir: %w", err)
	}
	args := []string{"restore", "--images-dir", imgDir, "--shell-job", "--tcp-established", "--restore-detached"}
	if out, err := exec.CommandContext(ctx, criu, args...).CombinedOutput(); err != nil {
		return fmt.Errorf("criu restore: %v: %s", err, out)
	}
	return nil
}
