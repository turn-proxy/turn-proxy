package server

import (
	"context"
	"net"
	"net/netip"
	"time"

	"github.com/turn-proxy/turn-proxy/internal/bufpool"
	"github.com/turn-proxy/turn-proxy/internal/obfs"
)

type demux struct {
	sock     *net.UDPConn
	maxPeers int
	inboxCap int
	idle     time.Duration
	accept   chan *Peer
	peers    map[netip.AddrPort]*Peer
}

func newDemux(ctx context.Context, sock *net.UDPConn, maxPeers, inboxCap int, idle time.Duration) <-chan *Peer {
	d := &demux{
		sock:     sock,
		maxPeers: maxPeers,
		inboxCap: inboxCap,
		idle:     idle,
		accept:   make(chan *Peer, 64),
		peers:    map[netip.AddrPort]*Peer{},
	}
	stop := context.AfterFunc(ctx, func() { _ = sock.Close() })
	go func() {
		defer stop()
		defer close(d.accept)
		d.readLoop(ctx)
	}()
	return d.accept
}

func (d *demux) readLoop(ctx context.Context) {
	buf := make([]byte, obfs.MaxDatagram)
	for {
		n, src, err := d.sock.ReadFromUDPAddrPort(buf)
		if err != nil {
			return
		}
		if n == 0 {
			continue
		}
		if !obfs.MatchDTLS(buf[:n]) && !obfs.MatchSRTP(buf[:n]) {
			continue
		}

		peer := d.peerFor(ctx, src)
		if peer == nil {
			continue
		}

		bp := bufpool.Get(n)
		copy(*bp, buf[:n])
		peer.deliver(buf[:n], bp)
	}
}

func (d *demux) peerFor(ctx context.Context, src netip.AddrPort) *Peer {
	peer, ok := d.peers[src]
	if ok && peer.isClosed() {
		delete(d.peers, src)
		ok = false
	}
	if ok {
		return peer
	}

	d.pruneClosed()
	if len(d.peers) >= d.maxPeers {
		return nil
	}
	peer = newPeer(ctx, d.sock, src, d.inboxCap, d.idle)
	d.peers[src] = peer
	select {
	case d.accept <- peer:
		return peer
	default:
		_ = peer.Close()
		delete(d.peers, src)
		return nil
	}
}

func (d *demux) pruneClosed() {
	for k, p := range d.peers {
		if p.isClosed() {
			delete(d.peers, k)
		}
	}
}
