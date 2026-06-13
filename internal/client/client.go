package client

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"slices"
	"sync"
	"sync/atomic"
	"time"

	pdtls "github.com/pion/dtls/v3"

	"github.com/turn-proxy/turn-broker/proto"
	"github.com/turn-proxy/turn-proxy/internal/bufpool"
	"github.com/turn-proxy/turn-proxy/internal/config"
	"github.com/turn-proxy/turn-proxy/internal/dtls"
	"github.com/turn-proxy/turn-proxy/internal/obfs"
	"github.com/turn-proxy/turn-proxy/internal/relay"
)

const (
	clientReadTimeout     = 60 * time.Second
	streamStagger         = 200 * time.Millisecond
	queueCap              = 2048
	reconnectBackoffMin   = 500 * time.Millisecond
	reconnectBackoffMax   = 10 * time.Second
	healthySession        = 5 * time.Second
	targetRefreshInterval = 60 * time.Second
)

type streamTarget struct {
	session    *proto.Session
	serverAddr *net.UDPAddr
}

type targetStore struct {
	cur       atomic.Pointer[streamTarget]
	ready     chan struct{}
	readyOnce sync.Once
}

func newTargetStore() *targetStore {
	return &targetStore{ready: make(chan struct{})}
}

func (s *targetStore) set(t *streamTarget) {
	s.cur.Store(t)
	s.readyOnce.Do(func() { close(s.ready) })
}

func (s *targetStore) get() *streamTarget {
	return s.cur.Load()
}

func (s *targetStore) wait(ctx context.Context) *streamTarget {
	select {
	case <-s.ready:
		return s.get()
	case <-ctx.Done():
		return nil
	}
}

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

	queue := make(chan *[]byte, queueCap)
	localPeer := &replyAddr{}

	go readLocal(ctx, local, queue, localPeer)

	store := newTargetStore()
	go resolveTargetLoop(ctx, cfg, store)

	var wg sync.WaitGroup
	for idx := range n {
		wg.Go(func() {
			select {
			case <-time.After(streamStagger * time.Duration(idx)):
			case <-ctx.Done():
				return
			}
			streamSupervisor(ctx, cfg, idx, store, queue, localPeer, local)
		})
	}
	wg.Wait()
	return nil
}

func readLocal(ctx context.Context, local *net.UDPConn, queue chan<- *[]byte, localPeer *replyAddr) {
	buf := make([]byte, obfs.MaxDatagram)
	for {
		_ = local.SetReadDeadline(time.Now().Add(time.Second))
		n, from, err := local.ReadFromUDP(buf)
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
		localPeer.Store(from)
		bp := bufpool.Get(n)
		copy(*bp, buf[:n])
		select {
		case queue <- bp:
		default:
			bufpool.Put(bp)
		}
	}
}

func resolveTargetLoop(ctx context.Context, cfg config.Config, store *targetStore) {
	for {
		if t, err := resolveTarget(ctx, cfg); err != nil {
			slog.Warn("resolve failed", "err", err)
		} else {
			store.set(t)
		}
		select {
		case <-time.After(targetRefreshInterval):
		case <-ctx.Done():
			return
		}
	}
}

func resolveTarget(ctx context.Context, cfg config.Config) (*streamTarget, error) {
	serverAddr, err := net.ResolveUDPAddr("udp", cfg.Upstream)
	if err != nil {
		return nil, fmt.Errorf("resolving server address %s: %w", cfg.Upstream, err)
	}
	session, err := getSession(ctx, cfg.Broker.URL, cfg.Broker.JoinLink)
	if err != nil {
		return nil, fmt.Errorf("fetching broker session: %w", err)
	}
	if len(session.URLs) == 0 {
		return nil, fmt.Errorf("broker session: no turn urls advertised")
	}
	return &streamTarget{session: session, serverAddr: serverAddr}, nil
}

func streamSupervisor(ctx context.Context, cfg config.Config, idx int, store *targetStore, queue chan *[]byte, localPeer *replyAddr, local *net.UDPConn) {
	backoff := reconnectBackoffMin
	for ctx.Err() == nil {
		start := time.Now()
		if err := runOneStream(ctx, cfg, idx, store, queue, localPeer, local); err != nil {
			slog.Warn("stream failed", "stream", idx, "err", err)
		} else if time.Since(start) >= healthySession {
			backoff = reconnectBackoffMin
		}
		if ctx.Err() != nil {
			return
		}
		select {
		case <-time.After(backoff):
		case <-ctx.Done():
			return
		}
		backoff *= 2
		if backoff > reconnectBackoffMax {
			backoff = reconnectBackoffMax
		}
	}
}

func rotateLeft(servers []string, k int) []string {
	n := len(servers)
	if n == 0 {
		return servers
	}
	k %= n
	return slices.Concat(servers[k:], servers[:k])
}

func runOneStream(ctx context.Context, cfg config.Config, idx int, store *targetStore, queue chan *[]byte, localPeer *replyAddr, local *net.UDPConn) error {
	target := store.wait(ctx)
	if target == nil {
		return nil
	}

	relayCfg := &relay.Config{
		Servers:     rotateLeft(target.session.URLs, idx),
		Username:    target.session.Username,
		Password:    target.session.Credential,
		Transport:   cfg.Transport(),
		ReadTimeout: clientReadTimeout,
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

	relayStreamToLocal(ctx, tunnel, queue, localPeer, local)
	return nil
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
