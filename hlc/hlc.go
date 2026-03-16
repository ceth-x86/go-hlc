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
//
// NOTE: Fields are exported for easy serialization and use in distributed systems,
// which means users can bypass validation by manually constructing the struct.
// It is recommended to use ParseTimestamp or the HLC methods for safety.
type Timestamp struct {
	// WallTime represents physical time in nanoseconds (Unix epoch).
	WallTime int64
	// Logical is a counter to differentiate events that occur at the same physical time.
	Logical int32
}

// Before returns true if t is before other.
func (t Timestamp) Before(other Timestamp) bool {
	return Compare(t, other) < 0
}

// After returns true if t is after other.
func (t Timestamp) After(other Timestamp) bool {
	return Compare(t, other) > 0
}

// Equal returns true if t is equal to other.
func (t Timestamp) Equal(other Timestamp) bool {
	return Compare(t, other) == 0
}

// HLC represents the Hybrid Logical Clock state.
type HLC struct {
	// latestTimestamp is the highest Timestamp generated or observed by this node so far.
	latestTimestamp Timestamp
	// maxOffset is the maximum allowed difference between a received timestamp and local wall time.
	maxOffset time.Duration
	// mu protects the HLC state during Now() and Update() calls.
	mu sync.Mutex
	// timeSource is used to mock physical time in tests.
	timeSource func() int64
}

// New creates a new Hybrid Logical Clock with the given maximum offset.
func New(maxOffset time.Duration) *HLC {
	return NewWithTimeSource(maxOffset, nil)
}

// NewWithTimeSource creates a new Hybrid Logical Clock with a custom time source.
// Useful for testing clock drift and specific time scenarios.
//
// Fallbacks:
//   - If source is nil, it defaults to time.Now().UnixNano().
//   - If maxOffset <= 0, it defaults to 500ms.
func NewWithTimeSource(maxOffset time.Duration, source func() int64) *HLC {
	if source == nil {
		source = func() int64 { return time.Now().UnixNano() }
	}
	if maxOffset <= 0 {
		maxOffset = 500 * time.Millisecond
	}
	return &HLC{
		maxOffset:  maxOffset,
		timeSource: source,
	}
}

// Provider defines the runtime interface for a Hybrid Logical Clock.
type Provider interface {
	// Now generates a new monotonic timestamp for a local event.
	Now() (Timestamp, error)
	// Update incorporates causality from a remote timestamp.
	Update(remote Timestamp) error
	// Latest returns the current highest timestamp thread-safely.
	Latest() Timestamp
}

// Ensure HLC implements the Provider interface
var _ Provider = (*HLC)(nil)

// Latest returns the current highest timestamp thread-safely.
func (h *HLC) Latest() Timestamp {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.latestTimestamp
}

// Now generates a monotonic timestamp for a local event.
// Returns an error if the logical counter reaches math.MaxInt32.
func (h *HLC) Now() (Timestamp, error) {
	h.mu.Lock()
	defer h.mu.Unlock()

	physicalNow := h.timeSource()
	latest := h.latestTimestamp

	if physicalNow > latest.WallTime {
		h.latestTimestamp = Timestamp{WallTime: physicalNow, Logical: 0}
	} else {
		// Overflow Protection
		if latest.Logical == math.MaxInt32 {
			return Timestamp{}, fmt.Errorf("HLC logical counter overflow in Now")
		}
		h.latestTimestamp = Timestamp{WallTime: latest.WallTime, Logical: latest.Logical + 1}
	}

	return h.latestTimestamp, nil
}

// Update updates the local clock based on a remote timestamp.
// It ensures the local clock stays monotonic and incorporates causality from the remote node.
// Returns an error if the remote timestamp exceeds maxOffset (bi-directional skew check),
// if the timestamp is a zero-value, or if the logical counter overflows.
func (h *HLC) Update(remote Timestamp) error {
	if remote.WallTime == 0 && remote.Logical == 0 {
		return fmt.Errorf("invalid remote timestamp: cannot be zero-value")
	}
	if remote.WallTime < 0 || remote.Logical < 0 {
		return fmt.Errorf("invalid remote timestamp: values cannot be negative")
	}

	h.mu.Lock()
	defer h.mu.Unlock()

	physicalNow := h.timeSource()
	latest := h.latestTimestamp

	// Bi-directional Clock Skew Check.
	// NOTE: We check boundaries explicitly to avoid issues with math.MinInt64 negation overflow.
	diff := remote.WallTime - physicalNow
	maxAllowed := int64(h.maxOffset)
	if diff > maxAllowed || diff < -maxAllowed {
		return fmt.Errorf("clock offset exceeded: skew is %v, max allowed is %v", time.Duration(diff), h.maxOffset)
	}

	// Determine the new WallTime: max(latest.WallTime, remote.WallTime, physicalNow)
	newWallTime := latest.WallTime
	if remote.WallTime > newWallTime {
		newWallTime = remote.WallTime
	}
	if physicalNow > newWallTime {
		newWallTime = physicalNow
	}

	// Determine the new Logical counter.
	var newLogical int32
	if newWallTime == latest.WallTime && newWallTime == remote.WallTime {
		if latest.Logical > remote.Logical {
			newLogical = latest.Logical
		} else {
			newLogical = remote.Logical
		}

		if newLogical == math.MaxInt32 {
			return fmt.Errorf("HLC logical counter overflow in Update")
		}
		newLogical++
	} else if newWallTime == latest.WallTime {
		if latest.Logical == math.MaxInt32 {
			return fmt.Errorf("HLC logical counter overflow in Update")
		}
		newLogical = latest.Logical + 1
	} else if newWallTime == remote.WallTime {
		if remote.Logical == math.MaxInt32 {
			return fmt.Errorf("HLC logical counter overflow in Update")
		}
		newLogical = remote.Logical + 1
	} else {
		newLogical = 0
	}

	h.latestTimestamp = Timestamp{WallTime: newWallTime, Logical: newLogical}
	return nil
}

// Restore sets the clock to a previously persisted state (Lower Bound).
// This prevents the clock from starting behind its previous state after a reboot.
//
// NOTE: It is assumed that 'state' is a valid previously generated HLC timestamp.
// It is intentionally NOT part of the Provider interface as it's a management/init
// operation, not a runtime clock operation.
func (h *HLC) Restore(state Timestamp) {
	if state.WallTime < 0 || state.Logical < 0 {
		return // Defensive guard against invalid inputs
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	if Compare(state, h.latestTimestamp) > 0 {
		h.latestTimestamp = state
	}
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

// String serializes the timestamp into a string format "WallTime_Logical".
func (t Timestamp) String() string {
	return fmt.Sprintf("%d_%d", t.WallTime, t.Logical)
}

// ParseTimestamp parses a string (format "WallTime_Logical") into a Timestamp.
func ParseTimestamp(s string) (Timestamp, error) {
	parts := strings.Split(s, "_")
	if len(parts) != 2 {
		return Timestamp{}, fmt.Errorf("invalid timestamp format: expected WallTime_Logical")
	}

	wallTime, err := strconv.ParseInt(parts[0], 10, 64)
	if err != nil {
		return Timestamp{}, fmt.Errorf("invalid WallTime: %v", err)
	}
	if wallTime < 0 {
		return Timestamp{}, fmt.Errorf("invalid WallTime: cannot be negative")
	}

	logical, err := strconv.ParseInt(parts[1], 10, 32)
	if err != nil {
		return Timestamp{}, fmt.Errorf("invalid Logical: %v", err)
	}
	if logical < 0 {
		return Timestamp{}, fmt.Errorf("invalid Logical: counter cannot be negative")
	}

	return Timestamp{
		WallTime: wallTime,
		Logical:  int32(logical),
	}, nil
}
