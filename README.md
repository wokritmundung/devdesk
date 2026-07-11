# rewarm

Stateful preemption for LLM inference on Kubernetes. Checkpoint and restore GPU
inference state (KV cache, engine state) at preemption time — spot GPU economics
without cold starts.

- **Spec:** [docs/one-pager.md](docs/one-pager.md)
- **Status:** pre-M1 (design + diagnostics)

## Layout

    cmd/preemption-hook/   Cluster controller (KAI/Kueue hooks, spot notices, CheckpointPolicy CRD)
    cmd/node-agent/        Privileged DaemonSet (checkpoint/restore, NVMe staging)
    cmd/dra-driver/        Optional DRA shim (placement hints)
    charts/rewarm/         Helm chart
    hack/diagnostics/      Cold-start diagnostic for design partners
