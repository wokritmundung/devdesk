# Cold-start diagnostic (pre-M1)

Goal: quantify a team's preemption pain before writing controller code.
Simulates a spot interruption against a long-context vLLM workload and reports:
time-to-first-token after restart, weight-load time, prefill-recompute time,
and estimated sessions lost per interruption. Output: a shareable report.

Planned: single Python script, stdlib + requests only.
