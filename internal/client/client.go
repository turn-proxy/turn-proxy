package client

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"math/rand/v2"
	"net"
	"slices"
	"sync"
	"time"

	pdtls "github.com/pion/dtls/v3"

	"github.com/turn-proxy/turn-proxy/internal/config"
	"github.com/turn-proxy/turn-proxy/internal/dtls"
	"github.com/turn-proxy/turn-proxy/internal/obfs"
)

const (
	streamStagger         = 200 * time.Millisecond
	queueCap              = 2048
	reconnectBackoffMin   = 500 * time.Millisecond
	reconnectBackoffMax   = 10 * time.Second
	quotaBackoff          = 30 * time.Second
	healthySession        = 5 * time.Second
	targetRefreshInterval = 60 * time.Second
)

func Run(ctx context.Context, cfg config.Config) error {
	laddr, err := net.ResolveUDPAddr("udp", cfg.Listen)
	if err != nil {
		return fmt.Errorf("resolve listen %s: %w", cfg.Listen, err)
	}
	local, err := net.ListenUDP("udp", laddr)
	if err != nil {
		return fmt.Errorf("binding client listener %s: %w", cfg.Listen, err)
	}
	defer local.Close()

	n := cfg.Turn.Streams
	slog.Info("starting", "mode", "client", "listen", cfg.Listen, "streams", n)

	resolver := newResolver(cfg.DNS.Servers)
	targets := newTargetResolver(resolver, cfg.Broker, cfg.Upstream)
	p := newStreamPool(local, targets, cfg.Transport())

	go p.readLocal(ctx)
	go targets.run(ctx)

	var wg sync.WaitGroup
	for idx := range n {
		wg.Go(func() {
			select {
			case <-time.After(streamStagger * time.Duration(idx)):
			case <-ctx.Done():
				return
			}
			p.supervise(ctx, idx)
		})
	}
	wg.Wait()
	return nil
}

func growBackoff(d time.Duration) time.Duration {
	d *= 2
	if d > reconnectBackoffMax {
		return reconnectBackoffMax
	}
	return d
}

func jitter(d time.Duration) time.Duration {
	if d <= 0 {
		return d
	}
	return d/2 + time.Duration(rand.Int64N(int64(d)))
}

func rotateLeft(servers []string, k int) []string {
	n := len(servers)
	if n == 0 {
		return servers
	}
	k %= n
	return slices.Concat(servers[k:], servers[:k])
}

type srtpTunnel struct {
	*obfs.SRTPConn
	dtls *pdtls.Conn
}

func (t *srtpTunnel) Close() error {
	_ = t.dtls.Close()
	return t.SRTPConn.Close()
}

func buildTunnel(ctx context.Context, relayConn net.PacketConn, serverAddr net.Addr) (io.ReadWriteCloser, error) {
	m := newMux(ctx, relayConn, serverAddr)
	conn, err := dtls.ClientHandshake(m.dtls, serverAddr, dtls.DefaultMTU, dtls.HandshakeTimeout)
	if err != nil {
		return nil, fmt.Errorf("dtls handshake: %w", err)
	}
	sender, receiver, err := obfs.DerivePair(conn, true)
	if err != nil {
		_ = conn.Close()
		return nil, fmt.Errorf("deriving srtp keys: %w", err)
	}
	return &srtpTunnel{SRTPConn: obfs.NewSRTPConn(m.srtp, serverAddr, sender, receiver), dtls: conn}, nil
}
