# Hybrid Logical Clock (HLC)

A thread-safe implementation of a **Hybrid Logical Clock (HLC)** in Go, based on the paper *"Logical Physical Clocks and Consistent Snapshots in Distributed Systems"*.

This clock provides monotonic timestamps that remain closely coupled with physical wall time while maintaining causal ordering across distributed nodes.

## Features

- **Causality Tracking:** Maintains `(WallTime, Logical)` tuple to order events correctly even if they happen within the same nanosecond.
- **Monotonicity:** Guarantees strictly increasing timestamps even if the system's physical clock drifts backward (NTP adjustments).
- **Drift Protection:** Rejects remote timestamps that exceed a configurable `MaxOffset` to prevent "runaway" clocks.
- **Thread-Safety:** Fully safe for concurrent use across multiple goroutines.
- **Persistence Support:** Includes `Restore` functionality to ensure the clock starts from a known lower bound after a reboot.

## Project Structure

```text
.
├── cmd/
│   └── hlc-demo/
│       └── main.go       # Demonstration utility
├── hlc/
│   ├── hlc.go            # Core HLC implementation
│   └── hlc_test.go       # Comprehensive test suite (drift, race, overflow)
├── go.mod                # Go module definition
└── specification.md      # Technical specification and roadmap
```

## Getting Started

### Prerequisites

- Go 1.21 or higher

### Running the Demonstration

To see the HLC in action (handling normal progression, rapid events, clock drift, and offset protection):

```bash
go run cmd/hlc-demo/main.go
```

### Running Tests

The project includes a robust test suite that covers concurrency, edge cases like logical counter overflow, and physical clock regressions.

```bash
# Run all tests
go test -v ./...

# Run tests with race detector
go test -v -race ./hlc
```

## Core API

### `New(maxOffset time.Duration) *HLC`
Creates a new HLC instance with a defined tolerance for clock skew.

### `Now() Timestamp`
Generates a new monotonic timestamp for a local event.

### `Update(remote Timestamp) error`
Updates the local clock state using a timestamp received from another node.

### `Compare(t1, t2 Timestamp) int`
Standard comparison utility: returns `-1` if `t1 < t2`, `1` if `t1 > t2`, and `0` if equal.

## Comparison Logic

Timestamps are compared as a tuple:
1. Compare `WallTime` (Physical nanoseconds).
2. If `WallTime` is identical, compare the `Logical` counter.
