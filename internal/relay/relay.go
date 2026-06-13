package relay

import (
	"fmt"
	"log/slog"
	"net"
	"strconv"
	"time"

	"github.com/pion/logging"
	"github.com/pion/stun/v3"
	"github.com/pion/turn/v5"
)

const allocTimeout = 10 * time.Second

type Config struct {
	Servers     []string
	Username    string
	Password    string
	Transport   stun.ProtoType
	ReadTimeout time.Duration
}

type Allocation struct {
	Relay       net.PacketConn
	RelayedAddr net.Addr
	Selected    string

	client  *turn.Client
	rawConn net.PacketConn
	wire    net.PacketConn
}

func (a *Allocation) Close() {
	if a.rawConn != nil {
		_ = a.rawConn.Close()
	}
	if a.client != nil {
		a.client.Close()
	}
	if a.wire != nil {
		_ = a.wire.Close()
	}
}

func Allocate(cfg *Config) (*Allocation, error) {
	if len(cfg.Servers) == 0 {
		return nil, fmt.Errorf("no turn servers configured")
	}
	var lastErr error
	for _, server := range cfg.Servers {
		a, err := allocateOne(server, cfg)
		if err != nil {
			slog.Warn("turn allocate failed, trying next", "server", server, "err", err)
			lastErr = err
			continue
		}
		return a, nil
	}
	return nil, fmt.Errorf("all %d turn servers failed; last: %w", len(cfg.Servers), lastErr)
}

func allocateOne(server string, cfg *Config) (*Allocation, error) {
	uri, err := stun.ParseURI(server)
	if err != nil || uri.Scheme != stun.SchemeTypeTURN {
		return nil, fmt.Errorf("parse turn url %q", server)
	}
	hostPort := net.JoinHostPort(uri.Host, strconv.Itoa(uri.Port))

	wireConn, err := newWireConn(cfg.Transport, hostPort)
	if err != nil {
		return nil, err
	}

	logFactory := logging.NewDefaultLoggerFactory()
	logFactory.DefaultLogLevel = logging.LogLevelWarn

	client, err := turn.NewClient(&turn.ClientConfig{
		TURNServerAddr: hostPort,
		Conn:           wireConn,
		Username:       cfg.Username,
		Password:       cfg.Password,
		LoggerFactory:  logFactory,
	})
	if err != nil {
		_ = wireConn.Close()
		return nil, fmt.Errorf("new turn client: %w", err)
	}
	if err := client.Listen(); err != nil {
		client.Close()
		_ = wireConn.Close()
		return nil, fmt.Errorf("turn listen: %w", err)
	}

	relayConn, err := allocateWithTimeout(client)
	if err != nil {
		client.Close()
		_ = wireConn.Close()
		return nil, fmt.Errorf("turn allocate: %w", err)
	}

	wrapped := relayConn
	if cfg.ReadTimeout > 0 {
		wrapped = &timeoutConn{PacketConn: relayConn, readTimeout: cfg.ReadTimeout}
	}

	return &Allocation{
		Relay:       wrapped,
		RelayedAddr: relayConn.LocalAddr(),
		Selected:    server,
		client:      client,
		rawConn:     relayConn,
		wire:        wireConn,
	}, nil
}

func newWireConn(transport stun.ProtoType, hostPort string) (net.PacketConn, error) {
	if transport == stun.ProtoTypeTCP {
		conn, err := net.Dial("tcp", hostPort)
		if err != nil {
			return nil, fmt.Errorf("dial tcp %s: %w", hostPort, err)
		}
		if tcp, ok := conn.(*net.TCPConn); ok {
			_ = tcp.SetNoDelay(true)
		}
		return turn.NewSTUNConn(conn), nil
	}
	return net.ListenPacket("udp", "0.0.0.0:0")
}

type timeoutConn struct {
	net.PacketConn
	readTimeout time.Duration
}

func (c *timeoutConn) ReadFrom(b []byte) (int, net.Addr, error) {
	if c.readTimeout > 0 {
		if err := c.SetReadDeadline(time.Now().Add(c.readTimeout)); err != nil {
			return 0, nil, err
		}
	}
	return c.PacketConn.ReadFrom(b)
}

func allocateWithTimeout(client *turn.Client) (net.PacketConn, error) {
	type result struct {
		conn net.PacketConn
		err  error
	}
	ch := make(chan result, 1)
	go func() {
		conn, err := client.Allocate()
		ch <- result{conn, err}
	}()
	select {
	case r := <-ch:
		return r.conn, r.err
	case <-time.After(allocTimeout):
		return nil, fmt.Errorf("allocation timed out")
	}
}
