# rewarm — Stateful Preemption for LLM Inference on Kubernetes

**One-liner:** Checkpoint and restore GPU inference state (KV cache, engine state) at preemption time, so spot GPUs and aggressive bin-packing stop meaning cold starts. Restore in seconds within the cluster; in under a minute across node death.

---

## Problem

Spot GPU capacity trades at roughly a 60% discount to on-demand, but LLM inference can't use it: an interruption destroys warm model weights and KV cache, and recovery takes 30–120 s — unacceptable for latency-sensitive serving. The penalty is worst exactly where inference is growing fastest: long-context (100K+ tokens) and agentic workloads, where KV state is expensive or impossible to recompute cheaply via prefill. Existing schedulers (KAI, Kueue) decide *what* gets preempted; nothing owns *what happens to the state* when it does.

## Solution

A Kubernetes controller suite that intercepts preemption and node-termination signals, coordinates the inference engine to produce a consistent checkpoint of GPU state, tiers it to the right storage target, and restores it on reschedule. We do not build a scheduler, a gateway, or a serving engine — we make the existing ones preemption-safe.

## Architecture

**1. Preemption hook (controller, cluster-scoped).** Integrates with KAI Scheduler and Kueue via their eviction/preemption paths and with cloud interruption notices (EC2 2-min, GCP/Azure ~30 s). Replaces "SIGTERM and hope" with: pause engine → flush in-flight batches → checkpoint → release. Exposed as a CRD (`CheckpointPolicy`) so platform teams declare behavior per workload class.

**2. Node cache controller (privileged DaemonSet).** The node-local agent that touches GPU memory. Drives checkpoint/restore via `cuda-checkpoint`/CRIU-GPU primitives and engine-native cooperation (vLLM sleep/wake, KV-cache export APIs). Owns local NVMe staging and snapshot lifecycle. We deliberately do not pretend to be daemonless.

**3. DRA resource shim (thin, optional by construction).** A Dynamic Resource Allocation driver layer (K8s ≥1.34; DRA is the standard — device plugins are legacy) that exposes checkpoint capability and NVMe staging locality as structured device attributes, so scheduling decisions can prefer restore-compatible placements. Checkpoint/restore itself has no DRA dependency: on pre-1.34 clusters the product runs without placement hints (worse restore locality, everything else intact) — graceful degradation instead of a legacy device-plugin fallback. Not a standalone fractional-GPU product; HAMi/NVIDIA own that.

**4. Tiering & policy engine.** Decides per event: snapshot vs. recompute (prefill cost model), what to snapshot (full KV vs. shared prefix blocks), compression, and destination tier. Two distinct recovery paths with different SLAs — this distinction is load-bearing:

| Event | State survives on | Restore target |
|---|---|---|
| Scheduler preemption (node lives) | Local NVMe | Seconds (same node or NVMe-peer) |
| Spot reclamation (node dies) | Remote object store / peer node, pushed within the warning window | Tens of seconds, onto warm-weight standby |

Sub-5-second recovery requires model weights already resident at the restore target; the controller manages a warm-pool to make that explicit rather than implied. Design target (to be validated in M3 benchmarks): with ~10% of GPU capacity kept warm, p90 recovery from node death <45 s — i.e., spot pricing on ~90% of the fleet, on-demand on the rest, sub-minute recovery everywhere.

## Target workloads (beachhead)

Long-context serving, agentic tool-loops, and shared-prefix multi-tenant serving on self-hosted vLLM — the segment where checkpoint decisively beats recompute and spot economics matter most. Short-context chat is explicitly *not* the wedge (prefill recompute often wins; Mooncake/LMCache cover tiering there).

**Primary buyer:** AI platform team lead at a company spending >$500K/year on GPU inference on self-managed Kubernetes, who has either restricted spot to batch workloads or ruled it out entirely because interruptions cold-start serving. Trigger moments: quarterly GPU cost review, or a postmortem after an interruption dropped live agent sessions.

## Non-goals

No custom scheduler (integrate with KAI/Kueue). No request routing (integrate with Gateway API Inference Extension via cache-affinity hints). No GPU slicing product. No CNI interaction — scheduling and GPU allocation never touch that layer.

## Deployment

Single Helm chart: controller Deployment, node DaemonSet (only on GPU nodes via selector), DRA driver, CRDs. Compatible with Karpenter/Cluster Autoscaler — the DRA attributes and standard taints keep provisioner decisions consistent with restore-aware placement.

## Open-core boundary

Open source: preemption hook, node agent, vLLM integration, local-NVMe path. Commercial: cross-node/cross-region restore, warm-pool management, the tiering cost model, fleet-wide savings attribution and audit.

## Key risks

Checkpoint consistency on a live engine is cooperative surgery, not a signal handler — vLLM integration depth is the schedule risk. `cuda-checkpoint`/CRIU-GPU are immature (also the moat). Snapshot size vs. escape bandwidth bounds the node-death path; the honest SLA is minutes-not-seconds there. NVIDIA (Dynamo/KAI) could absorb the category; speed to production references is the defense.

## Milestones

Pre-M1 (parallel, 0 code risk): ship a cold-start diagnostic script (simulate spot interruption on a long-context workload, emit recovery-time report); run it with 10 target platform teams to validate pain ranking and build the design-partner waitlist with real numbers. M1 (8 wks): prototype two checkpoint paths on one node with local NVMe — engine-native via vLLM sleep/wake + KV export (lighter, partial; the intra-cluster path) and process-level via CRIU/cuda-checkpoint (heavier, total; the node-death path) — pinned to a specific vLLM release. M2 (16 wks): KAI/Kueue hook + spot-notice handling, Helm chart, design-partner deploys. M3 (24 wks): remote tier + warm-pool restore, published recovery benchmarks vs. cold start, warm-pool target validated.
