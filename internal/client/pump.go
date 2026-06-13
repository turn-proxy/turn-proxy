package client

import (
	"context"
	"io"
	"log/slog"
	"net"
	"sync"
	"sync/atomic"

	"github.com/turn-proxy/turn-proxy/internal/bufpool"
	"github.com/turn-proxy/turn-proxy/internal/obfs"
)

type replyAddr struct {
	atomic.Pointer[net.UDPAddr]
}

func relayStreamToLocal(ctx context.Context, tunnel io.ReadWriteCloser, queue <-chan *[]byte, lp *replyAddr, local *net.UDPConn) {
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
			case bp := <-queue:
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
		if addr := lp.Load(); addr != nil {
			if _, err := local.WriteTo(buf[:n], addr); err != nil {
				slog.Warn("local udp send failed", "dst", addr, "err", err)
			}
		}
	}
}
