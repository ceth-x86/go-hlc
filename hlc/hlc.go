package hlc

import (
	"fmt"
	"math"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Timestamp represents a hybrid logical clock timestamp.
// It combines physical time and a logical counter.
type Timestamp struct {
	// WallTime represents physical time in nanoseconds (Unix epoch).
	WallTime int64
	// Logical is a counter to differentiate events that occur at the same physical time.
	Logical int32
}

// HLC represents the Hybrid Logical Clock state.
type HLC struct {
	// LatestTimestamp is the highest Timestamp generated or observed by this node so far.
	LatestTimestamp Timestamp
	// MaxOffset is the maximum allowed difference between a received timestamp and local wall time.
	MaxOffset time.Duration
	// mu protects the HLC state during Now() and Update() calls.
	mu sync.Mutex
	// timeSource is used to mock physical time in tests.
	timeSource func() int64
}

// New creates a new Hybrid Logical Clock with the given maximum offset.
func New(maxOffset time.Duration) *HLC {
	return &HLC{
		MaxOffset:  maxOffset,
		timeSource: func() int64 { return time.Now().UnixNano() },
	}
}

// Latest returns the current highest timestamp thread-safely.
func (h *HLC) Latest() Timestamp {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.LatestTimestamp
}

// Now generates a monotonic timestamp for a local event.
func (h *HLC) Now() Timestamp {
	h.mu.Lock()
	defer h.mu.Unlock()

	physicalNow := h.timeSource()
	latest := h.LatestTimestamp

	if physicalNow > latest.WallTime {
		h.LatestTimestamp = Timestamp{WallTime: physicalNow, Logical: 0}
	} else {
		// Overflow Protection: Handle the rare case where the Logical counter reaches its maximum value
		if latest.Logical == math.MaxInt32 {
			panic("HLC logical counter overflow")
		}
		h.LatestTimestamp = Timestamp{WallTime: latest.WallTime, Logical: latest.Logical + 1}
	}

	return h.LatestTimestamp
}

// Update updates the local clock based on a remote timestamp.
// It ensures the local clock stays monotonic and incorporates causality from the remote node.
// Returns an error if the remote timestamp is too far in the future (exceeds MaxOffset).
func (h *HLC) Update(remote Timestamp) error {
	h.mu.Lock()
	defer h.mu.Unlock()

	physicalNow := h.timeSource()
	latest := h.LatestTimestamp

	// Check for Clock Skew (insane remote clock)
	if remote.WallTime-physicalNow > int64(h.MaxOffset) {
		return fmt.Errorf("clock offset exceeded: remote timestamp is too far in the future")
	}

	// Determine the new WallTime: max(latest.WallTime, remote.WallTime, physicalNow)
	newWallTime := latest.WallTime
	if remote.WallTime > newWallTime {
		newWallTime = remote.WallTime
	}
	if physicalNow > newWallTime {
		newWallTime = physicalNow
	}

	// Determine the new Logical counter
	var newLogical int32
	if newWallTime == latest.WallTime && newWallTime == remote.WallTime {
		if latest.Logical > remote.Logical {
			newLogical = latest.Logical + 1
		} else {
			newLogical = remote.Logical + 1
		}
	} else if newWallTime == latest.WallTime {
		newLogical = latest.Logical + 1
	} else if newWallTime == remote.WallTime {
		newLogical = remote.Logical + 1
	} else {
		newLogical = 0
	}

	// Overflow Protection
	if newLogical < 0 {
		panic("HLC logical counter overflow")
	}

	h.LatestTimestamp = Timestamp{WallTime: newWallTime, Logical: newLogical}
	return nil
}

// Compare compares two timestamps, t1 and t2.
// It returns -1 if t1 < t2, 1 if t1 > t2, and 0 if t1 == t2.
func Compare(t1, t2 Timestamp) int {
	if t1.WallTime < t2.WallTime {
		return -1
	}
	if t1.WallTime > t2.WallTime {
		return 1
	}
	// WallTime is equal, compare Logical counter
	if t1.Logical < t2.Logical {
		return -1
	}
	if t1.Logical > t2.Logical {
		return 1
	}
	return 0
}

// String serializes the timestamp into a string format "WallTime.Logical".
func (t Timestamp) String() string {
	return fmt.Sprintf("%d.%d", t.WallTime, t.Logical)
}

// ParseTimestamp parses a string (format "WallTime.Logical") into a Timestamp.
func ParseTimestamp(s string) (Timestamp, error) {
	parts := strings.Split(s, ".")
	if len(parts) != 2 {
		return Timestamp{}, fmt.Errorf("invalid timestamp format: expected WallTime.Logical")
	}

	wallTime, err := strconv.ParseInt(parts[0], 10, 64)
	if err != nil {
		return Timestamp{}, fmt.Errorf("invalid WallTime: %v", err)
	}

	logical, err := strconv.ParseInt(parts[1], 10, 32)
	if err != nil {
		return Timestamp{}, fmt.Errorf("invalid Logical: %v", err)
	}

	return Timestamp{
		WallTime: wallTime,
		Logical:  int32(logical),
	}, nil
}

// Provider defines the interface for a Hybrid Logical Clock.
// This allows swapping between a real HLC and a mock one for testing.
type Provider interface {
	Now() Timestamp
	Update(remote Timestamp) error
	Latest() Timestamp
}

// Ensure HLC implements the Provider interface
var _ Provider = (*HLC)(nil)

// Restore sets the clock to a previously persisted state (Lower Bound).
// This prevents the clock from starting behind its previous state after a reboot.
func (h *HLC) Restore(state Timestamp) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if Compare(state, h.LatestTimestamp) > 0 {
		h.LatestTimestamp = state
	}
}
