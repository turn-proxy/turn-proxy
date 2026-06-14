package client

import (
	"testing"
	"time"
)

func TestJitterBounds(t *testing.T) {
	d := 4 * time.Second
	for range 1000 {
		j := jitter(d)
		if j < d/2 || j >= d+d/2 {
			t.Fatalf("jitter(%v) = %v out of [%v, %v)", d, j, d/2, d+d/2)
		}
	}
	if jitter(0) != 0 {
		t.Fatalf("jitter(0) = %v, want 0", jitter(0))
	}
}

func TestGrowBackoffCaps(t *testing.T) {
	d := reconnectBackoffMin
	for range 20 {
		d = growBackoff(d)
	}
	if d != reconnectBackoffMax {
		t.Fatalf("growBackoff saturated to %v, want %v", d, reconnectBackoffMax)
	}
}
