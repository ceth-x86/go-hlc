package hlc

import (
	"fmt"
	"math"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestCompare(t *testing.T) {
	tests := []struct {
		name     string
		t1       Timestamp
		t2       Timestamp
		expected int
	}{
		{"equal", Timestamp{100, 1}, Timestamp{100, 1}, 0},
		{"t1 earlier WallTime", Timestamp{99, 1}, Timestamp{100, 1}, -1},
		{"t1 later WallTime", Timestamp{101, 1}, Timestamp{100, 1}, 1},
		{"t1 earlier Logical", Timestamp{100, 0}, Timestamp{100, 1}, -1},
		{"t1 later Logical", Timestamp{100, 2}, Timestamp{100, 1}, 1},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := Compare(tt.t1, tt.t2)
			if result != tt.expected {
				t.Errorf("Compare() = %v, want %v", result, tt.expected)
			}
		})
	}
}

func TestTimestampHelpers(t *testing.T) {
	t1 := Timestamp{100, 1}
	t2 := Timestamp{100, 2}
	t3 := Timestamp{200, 0}

	if !t1.Before(t2) {
		t.Error("expected t1 < t2")
	}
	if !t2.Before(t3) {
		t.Error("expected t2 < t3")
	}
	if !t3.After(t1) {
		t.Error("expected t3 > t1")
	}
	if !t1.Equal(Timestamp{100, 1}) {
		t.Error("expected t1 == t1")
	}
}

func TestSerialization(t *testing.T) {
	ts := Timestamp{WallTime: 123456789, Logical: 42}
	str := ts.String()
	expectedStr := "123456789_42"

	if str != expectedStr {
		t.Errorf("String() = %v, want %v", str, expectedStr)
	}

	parsed, err := ParseTimestamp(str)
	if err != nil {
		t.Fatalf("ParseTimestamp() error = %v", err)
	}

	if Compare(ts, parsed) != 0 {
		t.Errorf("ParseTimestamp() = %v, want %v", parsed, ts)
	}

	errorTests := []struct {
		input string
		msg   string
	}{
		{"", "empty string"},
		{"invalid", "no underscore"},
		{"1_2_3", "too many underscores"},
		{"_0", "missing walltime"},
		{"100_", "missing logical"},
		{"100_-1", "negative logical"},
		{"-1_0", "negative walltime"},
		{"abc_0", "non-numeric walltime"},
		{"100_abc", "non-numeric logical"},
		{"9223372036854775808_0", "walltime overflow"},
		{"100_2147483648", "logical overflow"},
	}

	for _, tt := range errorTests {
		_, err = ParseTimestamp(tt.input)
		if err == nil {
			t.Errorf("ParseTimestamp(%q) expected error for %s", tt.input, tt.msg)
		}
	}
}

func TestHLC_Defaults(t *testing.T) {
	// Test nil timeSource and maxOffset <= 0 fallbacks
	hlc := NewWithTimeSource(0, nil)
	
	ts, err := hlc.Now()
	if err != nil {
		t.Fatalf("Now() failed with default source: %v", err)
	}
	if ts.WallTime == 0 {
		t.Error("expected non-zero WallTime from default source")
	}
	
	// Test bi-directional skew check with default 500ms offset
	// +600ms should fail
	remoteFuture := Timestamp{WallTime: ts.WallTime + int64(600*time.Millisecond), Logical: 0}
	if err := hlc.Update(remoteFuture); err == nil {
		t.Error("expected error for skew > 500ms (default offset)")
	}
}

func TestHLC_Now_NormalProgression(t *testing.T) {
	currentTime := int64(1000)
	timeSource := func() int64 { return currentTime }
	hlc := NewWithTimeSource(500*time.Millisecond, timeSource)

	ts1, err := hlc.Now()
	if err != nil {
		t.Fatalf("Now() failed: %v", err)
	}
	if ts1.WallTime != 1000 || ts1.Logical != 0 {
		t.Errorf("Expected {1000, 0}, got %v", ts1)
	}

	currentTime = 2000
	ts2, err := hlc.Now()
	if err != nil {
		t.Fatalf("Now() failed: %v", err)
	}
	if ts2.WallTime != 2000 || ts2.Logical != 0 {
		t.Errorf("Expected {2000, 0}, got %v", ts2)
	}
}

func TestHLC_Now_LogicalIncrement(t *testing.T) {
	currentTime := int64(1000)
	timeSource := func() int64 { return currentTime }
	hlc := NewWithTimeSource(500*time.Millisecond, timeSource)

	ts1, _ := hlc.Now()
	ts2, _ := hlc.Now()
	ts3, _ := hlc.Now()

	if ts1.Logical != 0 || ts2.Logical != 1 || ts3.Logical != 2 {
		t.Errorf("Expected logical counter to increment: %v, %v, %v", ts1, ts2, ts3)
	}
	if ts1.WallTime != 1000 || ts2.WallTime != 1000 || ts3.WallTime != 1000 {
		t.Errorf("Expected wall time to remain 1000")
	}
}

func TestHLC_Now_ClockDriftBackward(t *testing.T) {
	currentTime := int64(5000)
	timeSource := func() int64 { return currentTime }
	hlc := NewWithTimeSource(500*time.Millisecond, timeSource)

	ts1, _ := hlc.Now()

	// Simulate clock jumping backward
	currentTime = int64(5000) - int64(10*time.Second)

	ts2, _ := hlc.Now()
	ts3, _ := hlc.Now()

	if ts2.WallTime != 5000 || ts2.Logical != 1 {
		t.Errorf("Expected HLC to maintain forward progress despite backward drift, got %v", ts2)
	}
	if ts3.WallTime != 5000 || ts3.Logical != 2 {
		t.Errorf("Expected logical to keep incrementing, got %v", ts3)
	}

	// Ensure monotonicity
	if !ts1.Before(ts2) || !ts2.Before(ts3) {
		t.Errorf("Timestamps must be strictly monotonic: %v < %v < %v", ts1, ts2, ts3)
	}
}

func TestHLC_Now_ClockDriftForward(t *testing.T) {
	currentTime := int64(1000)
	timeSource := func() int64 { return currentTime }
	hlc := NewWithTimeSource(500*time.Millisecond, timeSource)

	ts1, _ := hlc.Now()

	// Simulate clock jumping forward by 10 seconds
	currentTime = int64(1000) + int64(10*time.Second)

	ts2, _ := hlc.Now()

	if ts2.WallTime != currentTime || ts2.Logical != 0 {
		t.Errorf("Expected HLC to adopt the new physical time and reset logical to 0, got %v", ts2)
	}

	// Ensure monotonicity
	if !ts1.Before(ts2) {
		t.Errorf("Timestamps must be strictly monotonic: %v < %v", ts1, ts2)
	}
}

func TestHLC_Update_Cases(t *testing.T) {
	timeSource := func() int64 { return 1000 }

	// Case 4: physicalNow leads both local and remote (newWallTime = physicalNow)
	// latest = {800, 5}, remote = {900, 10}, physicalNow = 1000
	hlc4 := NewWithTimeSource(500*time.Millisecond, timeSource)
	hlc4.Restore(Timestamp{WallTime: 800, Logical: 5})
	if err := hlc4.Update(Timestamp{WallTime: 900, Logical: 10}); err != nil {
		t.Fatalf("Update Case 4 failed: %v", err)
	}
	if hlc4.Latest().WallTime != 1000 || hlc4.Latest().Logical != 0 {
		t.Errorf("Expected Case 4 (reset to 0), got %v", hlc4.Latest())
	}

	// Case 1: All three match WallTime
	// latest = {1000, 5}, remote = {1000, 10}, physicalNow = 1000
	hlc1 := NewWithTimeSource(500*time.Millisecond, timeSource)
	hlc1.Restore(Timestamp{WallTime: 1000, Logical: 5})
	if err := hlc1.Update(Timestamp{WallTime: 1000, Logical: 10}); err != nil {
		t.Fatalf("Update Case 1 failed: %v", err)
	}
	if hlc1.Latest().WallTime != 1000 || hlc1.Latest().Logical != 11 {
		t.Errorf("Expected Case 1 (max + 1), got %v", hlc1.Latest())
	}

	// Case 1b: All three match WallTime, latest.Logical > remote.Logical
	// latest = {1000, 10}, remote = {1000, 5}, physicalNow = 1000
	hlc1b := NewWithTimeSource(500*time.Millisecond, timeSource)
	hlc1b.Restore(Timestamp{WallTime: 1000, Logical: 10})
	if err := hlc1b.Update(Timestamp{WallTime: 1000, Logical: 5}); err != nil {
		t.Fatalf("Update Case 1b failed: %v", err)
	}
	if hlc1b.Latest().WallTime != 1000 || hlc1b.Latest().Logical != 11 {
		t.Errorf("Expected Case 1b (latest.Logical + 1), got %v", hlc1b.Latest())
	}

	// Case 2: WallTime matches local only
	// latest = {2000, 5}, remote = {1500, 10}, physicalNow = 1000
	hlc2 := NewWithTimeSource(500*time.Millisecond, timeSource)
	hlc2.Restore(Timestamp{WallTime: 2000, Logical: 5})
	if err := hlc2.Update(Timestamp{WallTime: 1500, Logical: 10}); err != nil {
		t.Fatalf("Update Case 2 failed: %v", err)
	}
	if hlc2.Latest().WallTime != 2000 || hlc2.Latest().Logical != 6 {
		t.Errorf("Expected Case 2 (latest+1), got %v", hlc2.Latest())
	}

	// Case 3: WallTime matches remote only
	// latest = {1500, 5}, remote = {2000, 10}, physicalNow = 1000
	hlc3 := NewWithTimeSource(500*time.Millisecond, timeSource)
	hlc3.Restore(Timestamp{WallTime: 1500, Logical: 5})
	if err := hlc3.Update(Timestamp{WallTime: 2000, Logical: 10}); err != nil {
		t.Fatalf("Update Case 3 failed: %v", err)
	}
	if hlc3.Latest().WallTime != 2000 || hlc3.Latest().Logical != 11 {
		t.Errorf("Expected Case 3 (remote+1), got %v", hlc3.Latest())
	}
}

func TestHLC_Update_ClockSkew(t *testing.T) {
	currentTime := int64(1000)
	timeSource := func() int64 { return currentTime }
	hlc := NewWithTimeSource(500*time.Millisecond, timeSource)

	// Remote node is 1 second ahead (exceeds 500ms future)
	remoteFuture := Timestamp{WallTime: currentTime + int64(1*time.Second), Logical: 0}
	err := hlc.Update(remoteFuture)
	if err == nil {
		t.Error("Expected error for future skew exceeding MaxOffset")
	}

	// Remote node is 1 second behind (exceeds 500ms past)
	remotePast := Timestamp{WallTime: currentTime - int64(1*time.Second), Logical: 0}
	err = hlc.Update(remotePast)
	if err == nil {
		t.Error("Expected error for past skew exceeding MaxOffset")
	}

	// Extreme case: MinInt64 diff (should not panic and should fail skew check)
	extremePast := Timestamp{WallTime: math.MinInt64, Logical: 0}
	// We handle this defensively by checking remote.WallTime < 0 first
	err = hlc.Update(extremePast)
	if err == nil {
		t.Error("Expected error for negative WallTime")
	}

	// Zero-value rejection
	if err := hlc.Update(Timestamp{0, 0}); err == nil {
		t.Error("Expected error for zero-value timestamp in Update")
	}
}

func TestHLC_Update_OverflowError(t *testing.T) {
	timeSource := func() int64 { return 1000 }

	// Case 1: All equal and logical is max
	hlc1 := NewWithTimeSource(500*time.Millisecond, timeSource)
	hlc1.Restore(Timestamp{WallTime: 1000, Logical: math.MaxInt32})
	remote1 := Timestamp{WallTime: 1000, Logical: math.MaxInt32}
	if err := hlc1.Update(remote1); err == nil {
		t.Error("Expected error for logical overflow in Update (Branch 1: all equal)")
	}

	// Case 2: WallTime matches local only and local logical is max
	hlc2 := NewWithTimeSource(500*time.Millisecond, timeSource)
	hlc2.Restore(Timestamp{WallTime: 2000, Logical: math.MaxInt32})
	remote2 := Timestamp{WallTime: 1500, Logical: 0}
	if err := hlc2.Update(remote2); err == nil {
		t.Error("Expected error for logical overflow in Update (Branch 2: local wall matches)")
	}

	// Case 3: WallTime matches remote only and remote logical is max
	hlc3 := NewWithTimeSource(500*time.Millisecond, timeSource)
	hlc3.Restore(Timestamp{WallTime: 1500, Logical: 0})
	remote3 := Timestamp{WallTime: 2000, Logical: math.MaxInt32}
	if err := hlc3.Update(remote3); err == nil {
		t.Error("Expected error for logical overflow in Update (Branch 3: remote wall matches)")
	}
}

func TestHLC_Restore(t *testing.T) {
	hlc := New(500 * time.Millisecond)

	// Try restoring an older state, should be ignored
	olderState := Timestamp{WallTime: 500, Logical: 0}
	hlc.Restore(Timestamp{WallTime: 1000, Logical: 5}) // Initialize

	hlc.Restore(olderState)
	if hlc.Latest().WallTime != 1000 || hlc.Latest().Logical != 5 {
		t.Errorf("Expected older state to be ignored, got %v", hlc.Latest())
	}

	// Try restoring a newer state, should be adopted
	newerState := Timestamp{WallTime: 2000, Logical: 10}
	hlc.Restore(newerState)
	if hlc.Latest().WallTime != 2000 || hlc.Latest().Logical != 10 {
		t.Errorf("Expected newer state to be adopted, got %v", hlc.Latest())
	}
	
	// Test defensive negative guard
	hlc.Restore(Timestamp{WallTime: -1, Logical: 0})
	if hlc.Latest().WallTime != 2000 {
		t.Error("Restore should reject negative WallTime")
	}
}

func TestHLC_ConcurrencyStress(t *testing.T) {
	hlc := New(10 * time.Second)

	var wg sync.WaitGroup
	numRoutines := 50
	numOperations := 1000

	nowResults := make(chan Timestamp, numRoutines*numOperations)
	
	// Error collection for goroutines
	errChan := make(chan error, numRoutines)

	for i := 0; i < numRoutines; i++ {
		wg.Add(1)
		go func(routineID int) {
			defer wg.Done()
			var prev Timestamp
			var hasPrev bool
			for j := 0; j < numOperations; j++ {
				if j%2 == 0 {
					ts, err := hlc.Now()
					if err != nil {
						errChan <- fmt.Errorf("Now() failed: %v", err)
						return
					}
					// Per-goroutine monotonicity check
					if hasPrev && !prev.Before(ts) {
						errChan <- fmt.Errorf("Non-monotonic local sequence: %v -> %v", prev, ts)
						return
					}
					nowResults <- ts
					prev = ts
					hasPrev = true
				} else {
					remote := Timestamp{WallTime: time.Now().UnixNano(), Logical: int32(routineID + j)}
					if err := hlc.Update(remote); err != nil {
						// Update can fail due to clock skew in stress test, we just ignore skew errors
						// but check for critical errors like overflow
						if strings.Contains(err.Error(), "overflow") {
							errChan <- err
							return
						}
					}
				}
			}
		}(i)
	}

	wg.Wait()
	close(nowResults)
	close(errChan)

	// Check for any goroutine errors
	for err := range errChan {
		t.Errorf("Goroutine error: %v", err)
	}

	// Verify global uniqueness of all timestamps generated by Now()
	seen := make(map[string]bool)
	count := 0
	for ts := range nowResults {
		count++
		s := ts.String()
		if seen[s] {
			t.Errorf("Duplicate timestamp detected: %s", s)
		}
		seen[s] = true
	}

	expectedCount := numRoutines * (numOperations / 2)
	if count != expectedCount {
		t.Errorf("Expected %d Now() timestamps, got %d", expectedCount, count)
	}
}

func TestHLC_OverflowProtection(t *testing.T) {
	hlc := NewWithTimeSource(500*time.Millisecond, func() int64 { return 1000 })

	hlc.Restore(Timestamp{WallTime: 1000, Logical: math.MaxInt32})

	_, err := hlc.Now()
	if err == nil {
		t.Errorf("Expected error due to logical counter overflow in Now()")
	}
}
