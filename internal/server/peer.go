package server

import (
	"context"
	"net"
	"net/netip"
	"sync/atomic"
	"time"

	"github.com/turn-proxy/turn-proxy/internal/bufpool"
	"github.com/turn-proxy/turn-proxy/internal/obfs"
)

type Peer struct {
	sock       *net.UDPConn
	src        netip.AddrPort
	srcAddr    *net.UDPAddr
	local      net.Addr
	dtls       *obfs.Endpoint
	srtp       *obfs.Endpoint
	ctx        context.Context
	cancel     context.CancelFunc
	idle       time.Duration
	lastActive atomic.Int64
}

func newPeer(ctx context.Context, sock *net.UDPConn, src netip.AddrPort, inboxCap int, idle time.Duration) *Peer {
	ctx, cancel := context.WithCancel(ctx)
	peer := &Peer{
		sock:    sock,
		src:     src,
		srcAddr: net.UDPAddrFromAddrPort(src),
		local:   sock.LocalAddr(),
		ctx:     ctx,
		cancel:  cancel,
		idle:    idle,
	}
	peer.dtls = obfs.NewEndpoint(ctx, peer, peer.srcAddr, peer.local, inboxCap)
	peer.srtp = obfs.NewEndpoint(ctx, peer, peer.srcAddr, peer.local, inboxCap)
	peer.touch()
	go peer.watchIdle()
	return peer
}

func (p *Peer) touch() { p.lastActive.Store(time.Now().UnixNano()) }

func (p *Peer) idleElapsed() time.Duration {
	return time.Duration(time.Now().UnixNano() - p.lastActive.Load())
}

func (p *Peer) watchIdle() {
	ticker := time.NewTicker(p.idle)
	defer ticker.Stop()
	for {
		select {
		case <-p.ctx.Done():
			return
		case <-ticker.C:
			if p.idleElapsed() >= p.idle {
				_ = p.Close()
				return
			}
		}
	}
}

func (p *Peer) isClosed() bool { return p.ctx.Err() != nil }

func (p *Peer) deliver(buf []byte, bp *[]byte) {
	p.touch()
	switch {
	case obfs.MatchDTLS(buf):
		p.dtls.Deliver(bp)
	case obfs.MatchSRTP(buf):
		p.srtp.Deliver(bp)
	default:
		bufpool.Put(bp)
	}
}

func (p *Peer) WriteTo(b []byte, _ net.Addr) (int, error) {
	p.touch()
	return p.sock.WriteToUDPAddrPort(b, p.src)
}

func (p *Peer) Close() error {
	p.cancel()
	return nil
}
