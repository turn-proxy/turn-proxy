package server

import (
	"net"
	"net/netip"
	"testing"
	"time"

	"github.com/turn-proxy/turn-proxy/internal/bufpool"
	"github.com/turn-proxy/turn-proxy/internal/obfs"
)

func benchUDPConn(b *testing.B) *net.UDPConn {
	b.Helper()
	c, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		b.Fatalf("bind: %v", err)
	}
	b.Cleanup(func() { _ = c.Close() })
	return c
}

func BenchmarkServerDemuxFolded(b *testing.B) {
	sock := benchUDPConn(b)
	peer := newPeer(b.Context(), sock, netip.MustParseAddrPort("127.0.0.1:40000"), peerInboxCap, time.Hour)
	defer peer.Close()

	pkt := make([]byte, 1200)
	pkt[0] = 128
	rbuf := make([]byte, obfs.MaxDatagram)

	b.SetBytes(int64(len(pkt)))
	b.ReportAllocs()
	for b.Loop() {
		bp := bufpool.Get(len(pkt))
		copy(*bp, pkt)
		peer.deliver(pkt, bp)
		if _, _, err := peer.srtp.ReadFrom(rbuf); err != nil {
			b.Fatalf("read: %v", err)
		}
	}
}
