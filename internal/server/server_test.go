package server

import (
	"context"
	"net"
	"testing"
	"time"

	"github.com/turn-proxy/turn-proxy/internal/config"
)

func freeUDPAddr(t testing.TB) (*net.UDPAddr, string) {
	t.Helper()
	c, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		t.Fatalf("probe bind: %v", err)
	}
	addr := c.LocalAddr().(*net.UDPAddr)
	_ = c.Close()
	return addr, addr.String()
}

func waitBound(t testing.TB, laddr *net.UDPAddr) {
	t.Helper()
	for range 200 {
		c, err := net.ListenUDP("udp", laddr)
		if err != nil {
			return
		}
		_ = c.Close()
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("Serve never bound the listen port")
}

func TestServeDrainsLivePeerOnCancel(t *testing.T) {
	laddr, listen := freeUDPAddr(t)
	cfg := config.Config{
		Listen:   listen,
		Upstream: "127.0.0.1:9",
		Turn:     config.TurnSettings{PeerIdleTimeoutSecs: 600},
	}

	ctx, cancel := context.WithCancel(context.Background())
	errc := make(chan error, 1)
	go func() { errc <- Serve(ctx, cfg) }()

	waitBound(t, laddr)

	conn, err := net.DialUDP("udp", nil, laddr)
	if err != nil {
		t.Fatalf("dial server: %v", err)
	}
	defer conn.Close()
	if _, err := conn.Write([]byte{20, 1, 2, 3}); err != nil {
		t.Fatalf("send dtls-range packet: %v", err)
	}
	time.Sleep(200 * time.Millisecond)

	start := time.Now()
	cancel()
	select {
	case err := <-errc:
		if err != nil {
			t.Fatalf("Serve returned error: %v", err)
		}
		if elapsed := time.Since(start); elapsed > 5*time.Second {
			t.Fatalf("Serve took %v to drain (handshake timeout is 15s, idle 600s) — drain not working", elapsed)
		}
	case <-time.After(15 * time.Second):
		t.Fatal("Serve did not return within 15s of cancel — peer session leaked")
	}
}
