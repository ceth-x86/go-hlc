package hlc

import (
	"math"
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

func TestSerialization(t *testing.T) {
	ts := Timestamp{WallTime: 123456789, Logical: 42}
	str := ts.String()
	expectedStr := "123456789.42"

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

	_, err = ParseTimestamp("invalid")
	if err == nil {
		t.Errorf("ParseTimestamp(\"invalid\") expected error")
	}
}

func TestHLC_Now_NormalProgression(t *testing.T) {
	hlc := New(500 * time.Millisecond)
	
	// Mock time moving forward
	currentTime := int64(1000)
	hlc.timeSource = func() int64 {
		return currentTime
	}

	ts1 := hlc.Now()
	if ts1.WallTime != 1000 || ts1.Logical != 0 {
		t.Errorf("Expected {1000, 0}, got %v", ts1)
	}

	currentTime = 2000
	ts2 := hlc.Now()
	if ts2.WallTime != 2000 || ts2.Logical != 0 {
		t.Errorf("Expected {2000, 0}, got %v", ts2)
	}
}

func TestHLC_Now_LogicalIncrement(t *testing.T) {
	hlc := New(500 * time.Millisecond)
	
	currentTime := int64(1000)
	hlc.timeSource = func() int64 {
		return currentTime
	}

	ts1 := hlc.Now()
	ts2 := hlc.Now()
	ts3 := hlc.Now()

	if ts1.Logical != 0 || ts2.Logical != 1 || ts3.Logical != 2 {
		t.Errorf("Expected logical counter to increment: %v, %v, %v", ts1, ts2, ts3)
	}
	if ts1.WallTime != 1000 || ts2.WallTime != 1000 || ts3.WallTime != 1000 {
		t.Errorf("Expected wall time to remain 1000")
	}
}

func TestHLC_Now_ClockDriftBackward(t *testing.T) {
	hlc := New(500 * time.Millisecond)
	
	currentTime := int64(5000)
	hlc.timeSource = func() int64 {
		return currentTime
	}

	ts1 := hlc.Now()

	// Simulate clock jumping backward by 10 seconds
	currentTime = int64(5000) - int64(10*time.Second)
	
	ts2 := hlc.Now()
	ts3 := hlc.Now()

	if ts2.WallTime != 5000 || ts2.Logical != 1 {
		t.Errorf("Expected HLC to maintain forward progress despite backward drift, got %v", ts2)
	}
	if ts3.WallTime != 5000 || ts3.Logical != 2 {
		t.Errorf("Expected logical to keep incrementing, got %v", ts3)
	}
	
	// Ensure monotonicity
	if Compare(ts1, ts2) >= 0 || Compare(ts2, ts3) >= 0 {
		t.Errorf("Timestamps must be strictly monotonic: %v < %v < %v", ts1, ts2, ts3)
	}
}

func TestHLC_Now_ClockDriftForward(t *testing.T) {
	hlc := New(500 * time.Millisecond)
	
	currentTime := int64(1000)
	hlc.timeSource = func() int64 {
		return currentTime
	}

	ts1 := hlc.Now()

	// Simulate clock jumping forward by 10 seconds
	currentTime = int64(1000) + int64(10*time.Second)
	
	ts2 := hlc.Now()

	if ts2.WallTime != currentTime || ts2.Logical != 0 {
		t.Errorf("Expected HLC to adopt the new physical time and reset logical to 0, got %v", ts2)
	}
	
	// Ensure monotonicity
	if Compare(ts1, ts2) >= 0 {
		t.Errorf("Timestamps must be strictly monotonic: %v < %v", ts1, ts2)
	}
}

func TestHLC_Update_RemoteSync(t *testing.T) {
	hlc := New(500 * time.Millisecond)
	
	currentTime := int64(1000)
	hlc.timeSource = func() int64 { return currentTime }

	// Remote node is slightly ahead (within MaxOffset)
	remoteTs := Timestamp{WallTime: 2000, Logical: 5}
	err := hlc.Update(remoteTs)
	if err != nil {
		t.Fatalf("Unexpected error during Update: %v", err)
	}

	latest := hlc.LatestTimestamp
	if latest.WallTime != 2000 || latest.Logical != 6 {
		t.Errorf("Expected {2000, 6}, got %v", latest)
	}

	// Subsequence local event should use updated walltime
	localTs := hlc.Now()
	if localTs.WallTime != 2000 || localTs.Logical != 7 {
		t.Errorf("Expected {2000, 7}, got %v", localTs)
	}
}

func TestHLC_Update_ClockOffsetExceeded(t *testing.T) {
	hlc := New(500 * time.Millisecond)
	
	currentTime := int64(1000)
	hlc.timeSource = func() int64 { return currentTime }

	// Remote node is 1 second ahead, which exceeds 500ms
	remoteTs := Timestamp{WallTime: currentTime + int64(1*time.Second), Logical: 0}
	
	err := hlc.Update(remoteTs)
	if err == nil {
		t.Fatalf("Expected clock offset exceeded error")
	}
}

func TestHLC_ConcurrencyStress(t *testing.T) {
	hlc := New(10 * time.Second)
	
	var wg sync.WaitGroup
	numRoutines := 100
	numOperations := 1000

	timestamps := make(chan Timestamp, numRoutines*numOperations*2)

	for i := 0; i < numRoutines; i++ {
		wg.Add(1)
		go func(routineID int) {
			defer wg.Done()
			for j := 0; j < numOperations; j++ {
				// Mix of Now() and Update() calls
				if j%2 == 0 {
					timestamps <- hlc.Now()
				} else {
					remote := Timestamp{WallTime: time.Now().UnixNano(), Logical: int32(routineID + j)}
					_ = hlc.Update(remote)
					timestamps <- hlc.Latest()
				}
			}
		}(i)
	}

	wg.Wait()
	close(timestamps)

	count := 0
	for range timestamps {
		count++
	}
	
	if count != numRoutines*numOperations {
		t.Errorf("Expected %d timestamps, got %d", numRoutines*numOperations, count)
	}
}

func TestHLC_OverflowProtection(t *testing.T) {
	hlc := New(500 * time.Millisecond)
	hlc.timeSource = func() int64 { return 1000 }
	
	hlc.LatestTimestamp = Timestamp{WallTime: 1000, Logical: math.MaxInt32}
	
	defer func() {
		if r := recover(); r == nil {
			t.Errorf("Expected panic due to logical counter overflow")
		}
	}()
	
	hlc.Now()
}

func TestHLC_Restore(t *testing.T) {
	hlc := New(500 * time.Millisecond)
	
	// Try restoring an older state, should be ignored
	olderState := Timestamp{WallTime: 500, Logical: 0}
	hlc.LatestTimestamp = Timestamp{WallTime: 1000, Logical: 5}
	
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
}
