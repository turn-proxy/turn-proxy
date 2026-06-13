package server

import (
	"context"
	"net"
	"net/netip"
	"testing"
	"time"
)

func TestPeerIdleEviction(t *testing.T) {
	sock, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		t.Fatalf("bind: %v", err)
	}
	defer sock.Close()

	peer := newPeer(context.Background(), sock, netip.MustParseAddrPort("127.0.0.1:40000"), peerInboxCap, 50*time.Millisecond)
	defer peer.Close()

	select {
	case <-peer.ctx.Done():
	case <-time.After(2 * time.Second):
		t.Fatal("idle peer was not evicted by the watchdog")
	}
}

func TestPeerActiveNotEvicted(t *testing.T) {
	sock, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		t.Fatalf("bind: %v", err)
	}
	defer sock.Close()

	peer := newPeer(context.Background(), sock, netip.MustParseAddrPort("127.0.0.1:40001"), peerInboxCap, 80*time.Millisecond)
	defer peer.Close()

	deadline := time.After(300 * time.Millisecond)
	tick := time.NewTicker(20 * time.Millisecond)
	defer tick.Stop()
	for {
		select {
		case <-deadline:
			if peer.isClosed() {
				t.Fatal("active peer was evicted despite being touched")
			}
			return
		case <-tick.C:
			peer.touch()
		}
	}
}
