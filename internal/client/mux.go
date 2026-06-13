package client

import (
	"context"
	"log/slog"
	"net"

	"github.com/turn-proxy/turn-proxy/internal/bufpool"
	"github.com/turn-proxy/turn-proxy/internal/obfs"
)

type mux struct {
	dtls *obfs.Endpoint
	srtp *obfs.Endpoint
}

func newMux(ctx context.Context, inner net.PacketConn, remote net.Addr) *mux {
	local := inner.LocalAddr()
	ctx, cancel := context.WithCancel(ctx)
	m := &mux{
		dtls: obfs.NewEndpoint(ctx, inner, remote, local, obfs.DefaultInboxCapacity),
		srtp: obfs.NewEndpoint(ctx, inner, remote, local, obfs.DefaultInboxCapacity),
	}
	go m.readLoop(inner, cancel)
	return m
}

func (m *mux) dispatch(buf []byte) *obfs.Endpoint {
	switch {
	case obfs.MatchDTLS(buf):
		return m.dtls
	case obfs.MatchSRTP(buf):
		return m.srtp
	}
	return nil
}

func (m *mux) readLoop(inner net.PacketConn, cancel context.CancelFunc) {
	defer cancel()
	buf := make([]byte, obfs.MaxDatagram)
	for {
		n, _, err := inner.ReadFrom(buf)
		if err != nil {
			return
		}
		if n == 0 {
			continue
		}
		endpoint := m.dispatch(buf[:n])
		if endpoint == nil {
			slog.Debug("demux: dropping unclassified packet", "first", buf[0])
			continue
		}
		bp := bufpool.Get(n)
		copy(*bp, buf[:n])
		endpoint.Deliver(bp)
	}
}
