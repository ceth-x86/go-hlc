package main

import (
	"fmt"
	"hlc/hlc"
	"time"
)

func main() {
	fmt.Println("=== HLC (Hybrid Logical Clock) Demonstration ===")

	// 1. Initialize clock (MaxOffset = 500ms)
	maxOffset := 500 * time.Millisecond
	clock := hlc.New(maxOffset)

	// 2. Generate events in normal mode
	fmt.Println("\n--- Normal Event Generation ---")
	for i := 0; i < 3; i++ {
		ts := clock.Now()
		fmt.Printf("Event %d: Timestamp = %s\n", i+1, ts.String())
		time.Sleep(10 * time.Millisecond)
	}

	// 3. Demonstrate logical counter (simulating events within the same time tick)
	fmt.Println("\n--- Logical Increment (Rapid Events) ---")
	// Use a fresh clock for rapid generation
	mockClock := hlc.New(maxOffset)
	
	for i := 0; i < 5; i++ {
		ts := mockClock.Now()
		fmt.Printf("Fast Event %d: %s\n", i+1, ts)
	}

	// 4. Demonstrate clock drift backward (Monotonicity)
	fmt.Println("\n--- Clock Drift Backward (Monotonicity) ---")
	// Generate current state
	currentTs := clock.Now()
	fmt.Printf("Current HLC: %s\n", currentTs)
	
	// Simulate receiving a message from the "future" (within MaxOffset)
	// This forces the local clock's WallTime ahead of the system physical time.
	remoteFuture := hlc.Timestamp{WallTime: currentTs.WallTime + int64(100*time.Millisecond), Logical: 10}
	fmt.Printf("Received Remote (Future): %s\n", remoteFuture)
	
	err := clock.Update(remoteFuture)
	if err != nil {
		fmt.Printf("Update Error: %v\n", err)
	}
	
	fmt.Printf("Local HLC after Update: %s\n", clock.Latest())
	
	// Now system time is behind HLC WallTime, so Now() must increment Logical counter
	tsRegression := clock.Now()
	fmt.Printf("Next local event (System clock is behind HLC WallTime): %s\n", tsRegression)

	// 5. Demonstrate MaxOffset protection
	fmt.Println("\n--- Clock Offset Protection (MaxOffset Error) ---")
	insaneRemote := hlc.Timestamp{WallTime: time.Now().UnixNano() + int64(2*time.Second), Logical: 0}
	fmt.Printf("Trying to update with remote clock +2s (MaxOffset is 500ms): %s\n", insaneRemote)
	
	err = clock.Update(insaneRemote)
	if err != nil {
		fmt.Printf("Expected rejection: %v\n", err)
	}

	fmt.Println("\n=== Demo Finished ===")
}
