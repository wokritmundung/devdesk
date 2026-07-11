// Package main will hold the M2 preemption hook: the cluster controller that
// watches KAI/Kueue evictions and cloud interruption notices and drives the
// node-agent API (see pkg/agent). Deliberately empty in M1 — the manual
// trigger (rewarmctl) exercises the same agent contract.
package main
