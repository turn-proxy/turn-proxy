package server

import (
	"io"
	"log/slog"
	"net"

	"github.com/turn-proxy/turn-proxy/internal/obfs"
)

func relayToPeer(tunnel io.ReadWriteCloser, backend *net.UDPConn) {
	done := make(chan struct{}, 2)

	go func() {
		buf := make([]byte, obfs.MaxDatagram)
		for {
			n, err := tunnel.Read(buf)
			if err != nil {
				break
			}
			if n == 0 {
				continue
			}
			if _, err := backend.Write(buf[:n]); err != nil {
				slog.Warn("peer udp send failed", "err", err)
				break
			}
		}
		done <- struct{}{}
	}()

	go func() {
		buf := make([]byte, obfs.MaxDatagram)
		for {
			n, err := backend.Read(buf)
			if err != nil {
				break
			}
			if _, err := tunnel.Write(buf[:n]); err != nil {
				slog.Warn("tunnel send failed", "err", err)
				break
			}
		}
		done <- struct{}{}
	}()

	<-done
	_ = backend.Close()
	_ = tunnel.Close()
	<-done
}
