package dtls

import (
	"context"
	"net"
	"time"

	pdtls "github.com/pion/dtls/v3"
)

func ServerHandshake(conn net.PacketConn, rAddr net.Addr, mtu int, timeout time.Duration) (*pdtls.Conn, error) {
	opts, err := serverOptions(mtu)
	if err != nil {
		return nil, err
	}
	c, err := pdtls.ServerWithOptions(conn, rAddr, opts...)
	if err != nil {
		return nil, err
	}
	return handshake(c, timeout)
}

func ClientHandshake(conn net.PacketConn, rAddr net.Addr, mtu int, timeout time.Duration) (*pdtls.Conn, error) {
	opts, err := clientOptions(mtu)
	if err != nil {
		return nil, err
	}
	c, err := pdtls.ClientWithOptions(conn, rAddr, opts...)
	if err != nil {
		return nil, err
	}
	return handshake(c, timeout)
}

func handshake(c *pdtls.Conn, timeout time.Duration) (*pdtls.Conn, error) {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	if err := c.HandshakeContext(ctx); err != nil {
		_ = c.Close()
		return nil, err
	}
	return c, nil
}
