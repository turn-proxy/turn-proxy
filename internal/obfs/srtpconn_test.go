package obfs

import (
	"context"
	"net"
	"testing"
	"time"
)

func TestSRTPConnCloseUnblocksRead(t *testing.T) {
	sock, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		t.Fatal(err)
	}
	defer sock.Close()

	srtpPort := NewEndpoint(context.Background(), sock, sock.LocalAddr(), sock.LocalAddr(), DefaultInboxCapacity)
	conn := NewSRTPConn(srtpPort, sock.LocalAddr(), nil, nil)

	done := make(chan error, 1)
	go func() {
		buf := make([]byte, 1500)
		_, err := conn.Read(buf)
		done <- err
	}()

	time.Sleep(50 * time.Millisecond)
	if err := conn.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}

	select {
	case err := <-done:
		if err == nil {
			t.Fatal("expected Read to return an error after Close")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Read did not unblock after Close")
	}
}
