package obfs

import (
	"context"
	"errors"
	"os"
	"testing"
	"time"
)

func TestEndpointReadDeadline(t *testing.T) {
	e := NewEndpoint(context.Background(), nil, nil, nil, DefaultInboxCapacity)

	if err := e.SetReadDeadline(time.Now().Add(50 * time.Millisecond)); err != nil {
		t.Fatalf("set deadline: %v", err)
	}

	start := time.Now()
	_, _, err := e.ReadFrom(make([]byte, 64))
	if !errors.Is(err, os.ErrDeadlineExceeded) {
		t.Fatalf("expected deadline-exceeded, got %v", err)
	}
	if elapsed := time.Since(start); elapsed > time.Second {
		t.Fatalf("ReadFrom blocked %v past the deadline", elapsed)
	}
}

func TestEndpointReadNoDeadlineUnblockedByClose(t *testing.T) {
	e := NewEndpoint(context.Background(), nil, nil, nil, DefaultInboxCapacity)

	done := make(chan error, 1)
	go func() {
		_, _, err := e.ReadFrom(make([]byte, 64))
		done <- err
	}()

	time.Sleep(20 * time.Millisecond)
	_ = e.Close()

	select {
	case err := <-done:
		if err == nil {
			t.Fatal("expected error after Close")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("ReadFrom did not unblock after Close")
	}
}
