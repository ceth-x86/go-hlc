# Hybrid Logical Clock (HLC)

A thread-safe implementation of a **Hybrid Logical Clock (HLC)** in Go, based on the paper *"Logical Physical Clocks and Consistent Snapshots in Distributed Systems"*.

This clock provides monotonic timestamps that remain closely coupled with physical wall time while maintaining causal ordering across distributed nodes.

## Features

- **Causality Tracking:** Maintains `(WallTime, Logical)` tuple to order events correctly even if they happen within the same nanosecond.
- **Monotonicity:** Guarantees strictly increasing timestamps even if the system's physical clock drifts backward (NTP adjustments).
- **Bi-directional Drift Protection:** Rejects remote timestamps that deviate from local physical time by more than a configurable `maxOffset` (handles both future and past skew).
- **Thread-Safety:** Fully safe for concurrent use across multiple goroutines.
- **Persistence Support:** Includes `Restore` functionality to ensure the clock starts from a known lower bound after a reboot.

## How It Works

### The Problem

In a distributed system, you cannot rely on physical clocks alone to order events. Wall clocks across machines are never perfectly synchronized — NTP keeps them within a few milliseconds at best, but two nodes can easily produce events at the "same" nanosecond. Worse, clocks can jump backward due to NTP corrections, virtualization quirks, or timezone changes.

A pure logical clock (like Lamport's) solves ordering but loses all connection to real time, making it useless for queries like "give me all events from the last five minutes." The Hybrid Logical Clock bridges this gap: it stays as close to physical time as possible while guaranteeing the strict ordering properties of a logical clock.

### The Analogy

Think of a post office that stamps every letter with a timestamp. It has two tools:

- A **wall clock** — shows the current time, but occasionally drifts or jumps
- A **hand counter** — clicks +1 every time a new letter arrives within the same clock second

The rule is simple: every letter must receive a stamp that is strictly greater than the previous one. If the wall clock has moved forward, reset the counter and use the new time. If the wall clock is stale or has gone backward, keep the old time and click the counter. This way, no two letters ever share the same stamp, and the stamps always move forward.

### The Timestamp

An HLC timestamp is a pair of two numbers:

```
         Timestamp
    ┌─────────────────────┐
    │  WallTime (int64)   │  ← nanoseconds since Unix epoch
    │  Logical  (int32)   │  ← event counter within the same tick
    └─────────────────────┘

    Ordering: WallTime first, then Logical
    {1000, 0} < {1000, 1} < {1000, 2} < {2000, 0}
```

`WallTime` anchors the timestamp to physical reality. `Logical` differentiates events that happen at the same physical instant.

### Local Events: `Now()`

When a node needs a timestamp for a local event (a database write, a message send), `Now()` follows a two-branch rule:

```
    physicalNow = read system clock

    physicalNow > latest.WallTime?
         YES → {physicalNow, 0}         clock advanced, reset counter
         NO  → {latest.WallTime,        clock is stale or went backward,
                latest.Logical + 1}     keep old time, increment counter
```

If the system clock has moved forward, we adopt the new time and reset the counter to zero. If the clock is stale or has regressed (NTP correction, VM migration), we hold onto the last known wall time and simply increment the logical counter. Either way, the new timestamp is strictly greater than the previous one.

### Remote Events: `Update(remote)`

When a node receives a message from another node carrying a remote timestamp, `Update()` merges the causal histories. This is the heart of the HLC algorithm, taken from the Kulkarni et al. paper:

```
    Step 1: newWallTime = max(latest.WallTime, remote.WallTime, physicalNow)
    Step 2: Determine the logical counter (four cases)
```

The four cases for the logical counter:

```
┌─────────────────────────────────────────────────────────────────┐
│ Case 1: newWall ties with BOTH latest and remote                │
│         → max(latest.Logical, remote.Logical) + 1              │
│         "everyone sees the same wall time — take the highest    │
│          counter and advance it"                                │
│                                                                 │
│ Case 2: newWall ties with latest only                           │
│         → latest.Logical + 1                                   │
│         "local clock is the leader"                             │
│                                                                 │
│ Case 3: newWall ties with remote only                           │
│         → remote.Logical + 1                                   │
│         "remote clock is the leader"                            │
│                                                                 │
│ Case 4: newWall equals physicalNow (exceeds both)               │
│         → 0                                                    │
│         "physical time has overtaken everyone — fresh start"    │
└─────────────────────────────────────────────────────────────────┘
```

Before any of this runs, a bi-directional skew check rejects remote timestamps that differ from local physical time by more than `maxOffset`. This prevents a rogue or misconfigured node from dragging the local clock into an unreasonable state.

### A Full Cycle in a Distributed System

```
    Node A                              Node B
    ──────                              ──────
    Now() → {1000, 0}
                      ─── send msg ──→
                                        Update({1000, 0})
                                        physical = 1000
                                        newWall = max(0, 1000, 1000) = 1000
                                        case 3: remote wins → {1000, 1}

                                        Now() → {1000, 2}
                      ←── reply ──────
    Update({1000, 2})
    physical = 1001
    newWall = max(1000, 1000, 1001) = 1001
    case 4: physical wins → {1001, 0}   ← counter resets
```

Causal ordering is preserved: `{1000,0} < {1000,1} < {1000,2} < {1001,0}`.

### Safety Mechanisms

- **Overflow protection.** Every branch that increments the logical counter checks for `math.MaxInt32` before the increment and returns an error instead of silently wrapping around.
- **Bi-directional skew check.** Rejects remote timestamps that are too far in the future *or* too far in the past, preventing a single faulty node from corrupting the local clock.
- **Mutex guard.** Every method that touches the clock state acquires a lock, making the clock safe for concurrent use across dozens of goroutines.
- **Restore as a lower bound.** After a reboot, `Restore()` sets the clock to a previously persisted state, but never moves it backward — it only adopts the restored state if it is ahead of the current one.

### Why Not Just Use Physical Time?

```
The problem with wall clocks alone:
    Node A: write("x=1") at 12:00:00.000000001
    Node B: write("x=2") at 12:00:00.000000001  ← SAME TIME
    Who came first? Impossible to tell.

The HLC solution:
    Node A: write("x=1") → {12:00:00.000000001, 0}
    Node B receives A's message, then:
            write("x=2") → {12:00:00.000000001, 1}  ← UNIQUE
    Order is now unambiguous.
```

And the second trap: **clocks can go backward**. A bare `time.Now()` can return a value smaller than the previous call. The HLC handles this gracefully — it simply increments the logical counter while holding the wall time steady, guaranteeing that timestamps never regress.


## Project Structure

```text
.
├── cmd/
│   └── hlc-demo/
│       └── main.go       # Demonstration utility
├── hlc/
│   ├── hlc.go            # Core HLC implementation
│   └── hlc_test.go       # Comprehensive test suite
└── go.mod                # Go module definition
```

## Getting Started

### Prerequisites

- Go 1.26 or higher

### Running the Demonstration

To see the HLC in action (handling normal progression, rapid events, clock drift, and bi-directional offset protection):

```bash
go run cmd/hlc-demo/main.go
```

### Running Tests

The project includes a robust test suite covering concurrency, overflow, and skew validation.

```bash
go test -v ./...
```

## Core API

### `New(maxOffset time.Duration) *HLC`
Creates a new HLC instance with a defined tolerance for clock skew.

### `Now() (Timestamp, error)`
Generates a new monotonic timestamp for a local event. Returns an error if the logical counter overflows.

### `Update(remote Timestamp) error`
Updates the local clock state using a timestamp received from another node. Validates clock skew and counter overflow.

### `Latest() Timestamp`
Returns the current highest observed/generated timestamp thread-safely.

### `Compare(t1, t2 Timestamp) int`
Standard comparison utility: returns `-1` if `t1 < t2`, `1` if `t1 > t2`, and `0` if equal.

## Comparison Logic

Timestamps are compared as a tuple:
1. Compare `WallTime` (Physical nanoseconds).
2. If `WallTime` is identical, compare the `Logical` counter.

Syntactic sugar methods like `t.Before(other)`, `t.After(other)`, and `t.Equal(other)` are also available.
