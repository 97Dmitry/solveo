"""Real-time replay of measured decode costs on one serial model instance.

The benchmark runs decode steps back to back and records their compute time.
Replaying them against audio arrival times answers what a live worker would
see: a job can start only after its audio has arrived and the single model is
free. Two tracks share the same queue, which is how the planned worker
serializes decode calls.
"""

from __future__ import annotations

from dataclasses import dataclass


@dataclass(frozen=True)
class Job:
    arrival: float  # seconds of audio timeline when the job becomes runnable
    compute: float  # measured model seconds
    track: int


def finish_times(jobs: list[Job]) -> list[float]:
    """Finish time of each job, FIFO by arrival, ties by track."""
    order = sorted(range(len(jobs)), key=lambda i: (jobs[i].arrival, jobs[i].track, i))
    finish = [0.0] * len(jobs)
    free = 0.0
    for i in order:
        start = max(free, jobs[i].arrival)
        free = start + jobs[i].compute
        finish[i] = free
    return finish


def latencies(jobs: list[Job]) -> list[float]:
    return [done - job.arrival for job, done in zip(jobs, finish_times(jobs))]
