package housekeeping

import "testing"

func TestTransitionMatrix(t *testing.T) {
	legal := map[[2]Status]bool{
		{Dirty, Cleaning}:  true,
		{Cleaning, Clean}:  true,
		{Clean, Inspected}: true,
		{Dirty, Clean}:     true,
		{Cleaning, Dirty}:  true,
		{Clean, Dirty}:     true,
		{Inspected, Dirty}: true,
	}
	for _, from := range Statuses {
		for _, to := range Statuses {
			if got := CanTransition(from, to); got != legal[[2]Status{from, to}] {
				t.Errorf("CanTransition(%s, %s) = %v, want %v", from, to, got, !got)
			}
		}
	}
	if CanTransition("BOGUS", Dirty) || Status("BOGUS").Valid() {
		t.Error("unknown statuses are never legal")
	}
	for _, s := range Statuses {
		if s != Dirty && !CanTransition(s, Dirty) {
			t.Errorf("any→DIRTY must hold, %s→DIRTY does not", s)
		}
	}
}
