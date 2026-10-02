package main

import "testing"

// Every position on a floor has a type, and the ten positions spread 4/3/2/1 over the four types.
func TestTypeCodeAtSpreadsTheTenPositions(t *testing.T) {
	count := map[string]int{}
	for n := 1; n <= 10; n++ {
		count[typeCodeAt(n)]++
	}
	want := map[string]int{"STD": 4, "SUP": 3, "DLX": 2, "STE": 1}
	for code, n := range want {
		if count[code] != n {
			t.Fatalf("%s: %d rooms of ten, want %d (%v)", code, count[code], n, count)
		}
	}
}

func TestEveryTypeIsUsedOnce(t *testing.T) {
	seen := map[int]bool{}
	for _, spec := range types {
		for _, p := range spec.positions {
			if seen[p] {
				t.Fatalf("position %d is given to two types", p)
			}
			seen[p] = true
		}
	}
	if len(seen) != 10 {
		t.Fatalf("%d positions are covered, want 10", len(seen))
	}
}
