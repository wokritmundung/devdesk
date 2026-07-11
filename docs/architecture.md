# rewarm — Service Architecture Options

Companion to `docs/one-pager.md`. Three viable architectures for the checkpoint/restore
data plane, evaluated against the product's load-bearing constraints:

- **C1 — Two SLAs:** seconds for intra-cluster restore (node lives), <1 min across node death.
- **C2 — Escape window:** node-death state must leave the node within the cloud warning (~30 s GCP/Azure, 2 min AWS).
- **C3 — Zero-touch adoption:** platform teams should not have to modify workload pod specs to get value.
- **C4 — Single Helm chart:** operational footprint stays small; no exotic infra prerequisites for the open-source tier.
- **C5 — Non-goals hold:** no scheduler, no gateway, no GPU slicing.

---

## Design A — Sidecar-per-pod (cooperative, unprivileged)

A rewarm sidecar is injected (mutating webhook) into every vLLM pod. It talks to the
engine over localhost (sleep/wake, KV export APIs), writes snapshots to a node-local
NVMe volume, and reports to the cluster controller. No privileged node agent for the
engine-native path; the controller orchestrates preemption sequencing.

**Advantages.** Smallest security surface — no privileged DaemonSet, easiest to pass
enterprise review and to run on hardened/multi-tenant clusters. Per-pod blast radius:
a wedged sidecar affects one workload. Engine coupling is explicit and versioned per
pod (sidecar image pinned to engine version). Restore logic is symmetric: the new
pod's sidecar pulls the snapshot and replays it.

**Tradeoffs.** Process-level CRIU checkpointing is impossible from inside the pod —
this design can never serve the "total" node-death path with full engine state, only
KV-level snapshots. Violates C3: webhook injection touches every workload (and breaks
teams with their own webhook chains). No node-wide arbitration: N sidecars competing
for the same NVMe write bandwidth during a node-drain stampede is exactly the C2
scenario, unmanaged. Fleet upgrades churn every inference pod.

**Verdict.** Right answer only if the privileged DaemonSet proves unsellable in a
segment (e.g., regulated multi-tenant). Keep as a documented fallback mode, not the core.

---

## Design B — Node-agent centric (privileged DaemonSet data plane)

The one-pager's default, made concrete. A privileged DaemonSet on GPU nodes owns both
checkpoint paths: engine-native (vLLM API over local socket) and process-level
(cuda-checkpoint/CRIU-GPU). The cluster controller (preemption hook) sends signed gRPC
commands: `Checkpoint(pod, mode, deadline)`, `Restore(snapshot, target)`. The agent
owns local NVMe staging (simple local CSI/hostPath volume in OSS), enforces per-node
bandwidth budgets during drain stampedes, and streams to the remote tier for node-death
events. DRA driver colocates in the same agent binary.

**Advantages.** Zero-touch (C3): no workload mutation; adoption is `helm install` plus
a CheckpointPolicy CRD. Both checkpoint paths in one place, so the two-SLA table maps
1:1 onto code paths. Node-wide arbitration solves the drain-stampede problem centrally
— the agent sequences snapshot escapes by priority within the C2 window. One artifact
to qualify per kernel/driver matrix. Matches the repo layout as scaffolded
(`cmd/node-agent`, `cmd/preemption-hook`, `cmd/dra-driver`).

**Tradeoffs.** Privileged pod with CUDA and (for CRIU) ptrace-level access: the security
review is real, and a bug can affect every inference pod on the node. The
kernel × NVIDIA-driver × CUDA compatibility matrix concentrates in one binary — CRIU-GPU
fragility is the schedule risk called out in the one-pager, and here it is load-bearing.
Agent upgrades need node-by-node care (surge/drain awareness) to avoid self-inflicted
checkpoint gaps.

**Verdict.** The core architecture. Every constraint C1–C5 is satisfiable, and the
main risks (privilege, CRIU maturity) are the ones the product already owns as moat.

---

## Design C — Continuously-tiered KV plane (disaggregated cache)

Instead of checkpoint-on-signal, KV blocks replicate continuously to a cluster cache
tier (LMCache-style engine connector; NVMe-oF/RDMA pool or peer-node NVMe in the
commercial tier). Preemption becomes cheap by construction: kill the pod, state is
already off-node; restore = new pod attaches to the warm tier and pulls hot blocks
on demand.

**Advantages.** Dissolves C2 entirely — there is no escape scramble because state was
never node-captive; node death and scheduler preemption collapse into one path, one SLA.
Restore can be lazy (pull blocks as attention needs them), giving the best
time-to-first-token of the three. Unlocks adjacent value that Design B never can:
cross-pod prefix sharing, cache-aware routing hints to the gateway layer, warm
multi-model serving. This is the strongest platform story.

**Tradeoffs.** You pay always: replication burns network/CPU/PCIe on every request,
preemption or not — at low interruption rates, B's pay-on-event model is strictly
cheaper. Wants RDMA-class networking for good numbers; violates C4 for the median
self-hosted cluster. Deepest engine coupling (KV-connector internals, per-vLLM-version).
Only covers KV state — engine/scheduler state for in-flight requests still needs a
B-style mechanism, so C is an addition to B, not a replacement. And it walks directly
into LMCache/Mooncake/Dynamo territory, weakening the "we don't compete with the
serving stack" positioning.

**Verdict.** Not the wedge — the commercial-tier evolution. B's remote-tier streaming
becomes C's continuous replication for customers whose interruption rates and network
justify it.

---

## Comparison

| | A: Sidecar | B: Node agent | C: Tiered KV plane |
|---|---|---|---|
| Intra-cluster restore (C1) | Seconds | Seconds | Seconds (lazy pull) |
| Node-death path (C1/C2) | Weak (KV-only, unmanaged stampede) | <1 min, managed escape | Best; no escape needed |
| Zero-touch (C3) | No (webhook) | **Yes** | Partial (engine connector) |
| Ops footprint (C4) | Medium | **Low** | High (cache tier, fast net) |
| Security surface | **Smallest** | Privileged DaemonSet | Medium + new data service |
| Steady-state cost | Low | **Lowest (pay-on-event)** | Continuous replication tax |
| CRIU/total-state support | No | **Yes** | No (needs B anyway) |
| Platform upside | Low | Medium | **High** |

## Recommendation

Build **B** as the core (it is what the repo scaffold and one-pager already assume),
with two deliberate seams: (1) the agent's engine-native path speaks a narrow internal
interface so an unprivileged **A**-mode sidecar can implement the same interface for
restricted environments later; (2) the agent's remote-tier streamer is designed so
**C**'s continuous replication is a policy change ("stream always" vs. "stream on
signal"), not a rewrite. That sequencing keeps M1–M3 focused while leaving both the
regulated-enterprise door and the platform-story door open.
