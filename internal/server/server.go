package server

import (
	"context"
	"fmt"
	"log/slog"
	"net"
	"sync"
	"time"

	"github.com/turn-proxy/turn-proxy/internal/config"
	"github.com/turn-proxy/turn-proxy/internal/dtls"
	"github.com/turn-proxy/turn-proxy/internal/obfs"
)

const (
	peerInboxCap = 64
	maxPeers     = 128
)

func Serve(ctx context.Context, cfg config.Config) error {
	laddr, err := net.ResolveUDPAddr("udp", cfg.Listen)
	if err != nil {
		return fmt.Errorf("resolve listen %s: %w", cfg.Listen, err)
	}
	sock, err := net.ListenUDP("udp", laddr)
	if err != nil {
		return fmt.Errorf("binding public udp listener %s: %w", cfg.Listen, err)
	}
	slog.Info("server listening", "listen", cfg.Listen, "upstream", cfg.Upstream)

	idle := time.Duration(cfg.Turn.PeerIdleTimeoutSecs) * time.Second
	accept := newDemux(ctx, sock, maxPeers, peerInboxCap, idle)

	var wg sync.WaitGroup
	for peer := range accept {
		slog.Info("new peer accepted", "peer", peer.src)
		wg.Go(func() {
			if err := runPeerSession(peer, cfg.Upstream); err != nil {
				slog.Warn("peer session ended", "peer", peer.src, "err", err)
			}
		})
	}
	wg.Wait()
	return nil
}

func runPeerSession(peer *Peer, upstream string) error {
	defer peer.Close()

	conn, err := dtls.ServerHandshake(peer.dtls, peer.srcAddr, dtls.DefaultMTU, dtls.HandshakeTimeout)
	if err != nil {
		return fmt.Errorf("dtls accept: %w", err)
	}
	defer conn.Close()
	slog.Info("dtls handshake completed with peer", "peer", peer.src)
	sender, receiver, err := obfs.DerivePair(conn, false)
	if err != nil {
		return fmt.Errorf("deriving srtp keys: %w", err)
	}
	slog.Info("srtp keys derived from dtls keying material", "peer", peer.src)
	tunnel := obfs.NewSRTPConn(peer.srtp, peer.srcAddr, sender, receiver, true)

	backendAddr, err := net.ResolveUDPAddr("udp", upstream)
	if err != nil {
		return fmt.Errorf("resolve upstream %s: %w", upstream, err)
	}
	backend, err := net.DialUDP("udp", nil, backendAddr)
	if err != nil {
		return fmt.Errorf("dial upstream: %w", err)
	}

	relayToPeer(tunnel, backend)
	slog.Info("session closed", "peer", peer.src)
	return nil
}
