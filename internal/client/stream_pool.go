package client

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"sync"
	"sync/atomic"
	"time"

	"github.com/pion/stun/v3"

	"github.com/turn-proxy/turn-proxy/internal/bufpool"
	"github.com/turn-proxy/turn-proxy/internal/obfs"
	"github.com/turn-proxy/turn-proxy/internal/relay"
)

type streamPool struct {
	local     *net.UDPConn
	queue     chan *[]byte
	localPeer atomic.Pointer[net.UDPAddr]
	targets   *targetResolver
	transport stun.ProtoType
}

func newStreamPool(local *net.UDPConn, targets *targetResolver, transport stun.ProtoType) *streamPool {
	return &streamPool{
		local:     local,
		queue:     make(chan *[]byte, queueCap),
		targets:   targets,
		transport: transport,
	}
}

func (p *streamPool) readLocal(ctx context.Context) {
	buf := make([]byte, obfs.MaxDatagram)
	for {
		_ = p.local.SetReadDeadline(time.Now().Add(time.Second))
		n, from, err := p.local.ReadFromUDP(buf)
		if err != nil {
			if ctx.Err() != nil {
				return
			}
			if ne, ok := errors.AsType[net.Error](err); ok && ne.Timeout() {
				continue
			}
			slog.Warn("local udp recv failed", "err", err)
			return
		}
		p.localPeer.Store(from)
		bp := bufpool.Get(n)
		copy(*bp, buf[:n])
		select {
		case p.queue <- bp:
		default:
			bufpool.Put(bp)
		}
	}
}

func (p *streamPool) supervise(ctx context.Context, idx int) {
	backoff := reconnectBackoffMin
	for ctx.Err() == nil {
		start := time.Now()
		err := p.runStream(ctx, idx)

		var wait time.Duration
		switch {
		case err == nil:
			if time.Since(start) >= healthySession {
				backoff = reconnectBackoffMin
			}
			wait = backoff
			backoff = growBackoff(backoff)
		case relay.IsQuotaReached(err):
			slog.Warn("turn allocation quota reached", "stream", idx, "retry_in", quotaBackoff)
			wait = quotaBackoff
			backoff = reconnectBackoffMin
		default:
			slog.Warn("stream failed", "stream", idx, "err", err)
			wait = backoff
			backoff = growBackoff(backoff)
		}

		if ctx.Err() != nil {
			return
		}
		select {
		case <-time.After(jitter(wait)):
		case <-ctx.Done():
			return
		}
	}
}

func (p *streamPool) runStream(ctx context.Context, idx int) error {
	target := p.targets.wait(ctx)
	if target == nil {
		return nil
	}

	relayCfg := &relay.Config{
		Servers:   rotateLeft(target.session.URLs, idx),
		Username:  target.session.Username,
		Password:  target.session.Credential,
		Transport: p.transport,
	}
	slog.Info("stream connecting", "stream", idx, "server", target.serverAddr, "turn_url", relayCfg.Servers[0])

	allocated, err := relay.Allocate(relayCfg)
	if err != nil {
		return fmt.Errorf("allocating turn relay: %w", err)
	}
	defer allocated.Close()
	slog.Info("turn relay allocated", "relayed", allocated.RelayedAddr, "peer", target.serverAddr)

	tunnel, err := buildTunnel(ctx, allocated.Relay, target.serverAddr)
	if err != nil {
		return fmt.Errorf("building tunnel: %w", err)
	}

	p.relayStreamToLocal(ctx, tunnel)
	return nil
}

func (p *streamPool) relayStreamToLocal(ctx context.Context, tunnel io.ReadWriteCloser) {
	stop := make(chan struct{})
	var once sync.Once
	shut := func() {
		once.Do(func() {
			close(stop)
			_ = tunnel.Close()
		})
	}
	defer shut()

	go func() {
		select {
		case <-ctx.Done():
			shut()
		case <-stop:
		}
	}()

	go func() {
		for {
			select {
			case bp := <-p.queue:
				_, err := tunnel.Write(*bp)
				bufpool.Put(bp)
				if err != nil {
					shut()
					return
				}
			case <-stop:
				return
			}
		}
	}()

	buf := make([]byte, obfs.MaxDatagram)
	for {
		n, err := tunnel.Read(buf)
		if err != nil {
			return
		}
		if n == 0 {
			continue
		}
		if addr := p.localPeer.Load(); addr != nil {
			if _, err := p.local.WriteTo(buf[:n], addr); err != nil {
				slog.Warn("local udp send failed", "dst", addr, "err", err)
			}
		}
	}
}
