package common

import "testing"

func TestTimingStatCountsCalls(t *testing.T) {
	var stat TimingStat
	for i := 0; i < 3; i++ {
		stat.TimeOperation(func() {})
	}
	if stat.CallCount != 3 {
		t.Errorf("CallCount = %d, want 3", stat.CallCount)
	}
	if stat.IsTiming {
		t.Error("IsTiming should be cleared after the operation")
	}
}

func TestTimingStatHandlesReentrancy(t *testing.T) {
	// A nested TimeOperation must still count the call but must not restart or
	// double-count the timer, and must leave IsTiming set for the outer call.
	var stat TimingStat
	innerSawTiming := false

	stat.TimeOperation(func() {
		stat.TimeOperation(func() {
			innerSawTiming = stat.IsTiming
		})
		if !stat.IsTiming {
			t.Error("the nested call cleared the outer timing flag")
		}
	})

	if !innerSawTiming {
		t.Error("the nested call should have observed IsTiming set")
	}
	if stat.CallCount != 2 {
		t.Errorf("CallCount = %d, want 2", stat.CallCount)
	}
	if stat.IsTiming {
		t.Error("IsTiming should be cleared once the outer call returns")
	}
}

func TestSubtractFromTimeWhenNotTiming(t *testing.T) {
	var stat TimingStat
	called := false
	stat.SubtractFromTime(func() { called = true })
	if !called {
		t.Error("the callback must run even when not timing")
	}
	if stat.TotalTime != 0 {
		t.Errorf("TotalTime = %d, want 0", stat.TotalTime)
	}
}

func TestPrintTimeFormatting(t *testing.T) {
	// printTime rounds to two decimals and renders with Number.toString(),
	// which drops trailing zeros.
	for _, tt := range []struct {
		totalMs int64
		want    string
	}{
		{0, "0sec"},
		{1000, "1sec"},
		{1500, "1.5sec"},
		{1234, "1.23sec"},
		{1235, "1.24sec"},
		{100, "0.1sec"},
	} {
		stat := TimingStat{TotalTime: tt.totalMs}
		if got := stat.PrintTime(); got != tt.want {
			t.Errorf("PrintTime() for %dms = %q, want %q", tt.totalMs, got, tt.want)
		}
	}
}
