package simclock

import (
	"testing"
	"time"
)

func TestStateAt(t *testing.T) {
	real := time.Date(2026, 11, 1, 10, 0, 0, 0, time.UTC)
	sim := time.Date(2026, 12, 25, 6, 0, 0, 0, time.UTC)
	s := State{AnchorReal: real, AnchorSim: sim, Speed: 144}
	// 144×: ten real minutes is a simulated day.
	if got := s.At(real.Add(10 * time.Minute)); !got.Equal(sim.Add(24 * time.Hour)) {
		t.Errorf("144× after 10 min = %v, want a day later", got)
	}
	s.Paused = true
	if got := s.At(real.Add(time.Hour)); !got.Equal(sim) {
		t.Errorf("paused clock moved to %v", got)
	}
	s = State{AnchorReal: real, AnchorSim: real, Speed: 1}
	if got := s.At(real.Add(90 * time.Second)); !got.Equal(real.Add(90 * time.Second)) {
		t.Errorf("1× should track real time, got %v", got)
	}
}
