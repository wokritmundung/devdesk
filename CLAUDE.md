# rewarm

Stateful preemption for LLM inference on Kubernetes: checkpoint/restore GPU state
(KV cache + engine state) at preemption time so spot GPUs don't mean cold starts.
Canonical spec: `docs/one-pager.md` — read it before proposing architecture changes.

## Why

Spot GPUs are ~60% cheaper but interruptions cold-start inference (30–120s).
We make preemption survivable: seconds to restore within the cluster (local NVMe),
under a minute across node death (remote tier + warm pool).

## What (components)

- `cmd/preemption-hook/` — cluster controller; intercepts KAI/Kueue evictions and
  cloud spot notices; drives pause → flush → checkpoint → release. Owns the
  `CheckpointPolicy` CRD.
- `cmd/node-agent/` — privileged DaemonSet on GPU nodes; performs checkpoint/restore
  via cuda-checkpoint/CRIU-GPU and vLLM engine cooperation; manages local NVMe staging.
- `cmd/dra-driver/` — thin DRA shim (K8s ≥1.34); exposes checkpoint capability and
  NVMe locality as device attributes. OPTIONAL by construction — everything must
  work (degraded locality) without it. Never add hard DRA dependencies elsewhere.
- `charts/rewarm/` — single Helm chart: controller Deployment, node DaemonSet,
  DRA driver, CRDs.
- `hack/diagnostics/` — pre-M1 cold-start diagnostic script for design partners.

## How

- Language: Go (controllers, agent). Scripts in `hack/`: bash or Python, no deps
  beyond stdlib where possible.
- Build: `make build` · Test: `make test` · Lint: `make lint` (golangci-lint).
- Controllers use controller-runtime; follow kubebuilder layout conventions.
- Two checkpoint paths, keep them separate: engine-native (light/partial,
  intra-cluster) and process-level CRIU (heavy/total, node-death). Don't merge them.

## Non-goals (do not implement)

No custom scheduler (integrate with KAI/Kueue). No request routing/gateway.
No GPU slicing. No device plugin — DRA only, optional. No CNI interaction.

## Conventions

- Honest SLA language everywhere: "seconds" only for intra-cluster restore;
  "under a minute" for node death. Never claim sub-5s without warm weights.
- Every controller change needs an envtest; node-agent GPU paths get a fake-driver
  test double (real-GPU tests are manual, documented in the PR).
- Commits: conventional commits (feat/fix/docs/chore).

## M1 implementation notes

- Agent API is JSON/HTTP in M1 (`pkg/api` types are protobuf-friendly); the
  gRPC contract replaces the transport in M2 without changing semantics.
- `rewarmctl` is the M1 manual trigger; the M2 preemption hook must drive the
  exact same agent endpoints — no privileged side channels.
- `checkpoint.ErrUnavailable` is a contract: policy (onUnavailable: recompute)
  depends on paths reporting unavailability honestly instead of failing generically.
