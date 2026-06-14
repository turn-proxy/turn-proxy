package relay

import (
	"errors"
	"fmt"
	"log/slog"
	"net"
	"strconv"
	"time"

	"github.com/pion/logging"
	"github.com/pion/stun/v3"
	"github.com/pion/turn/v5"
)

const (
	allocTimeout        = 10 * time.Second
	permRefreshInterval = 30 * time.Minute
)

func IsQuotaReached(err error) bool {
	var te *stun.TurnError
	return errors.As(err, &te) && te.ErrorCodeAttr.Code == stun.CodeAllocQuotaReached
}

type Config struct {
	Servers   []string
	Username  string
	Password  string
	Transport stun.ProtoType
}

type Allocation struct {
	Relay       net.PacketConn
	RelayedAddr net.Addr
	Selected    string

	client *turn.Client
	wire   net.PacketConn
}

func (a *Allocation) Close() {
	if a.Relay != nil {
		_ = a.Relay.Close()
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
		TURNServerAddr:            hostPort,
		Conn:                      wireConn,
		Net:                       directNet{},
		Username:                  cfg.Username,
		Password:                  cfg.Password,
		RequestedAddressFamily:    turn.RequestedAddressFamilyIPv4,
		PermissionRefreshInterval: permRefreshInterval,
		LoggerFactory:             logFactory,
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

	relayConn, err := client.Allocate()
	if err != nil {
		client.Close()
		_ = wireConn.Close()
		return nil, fmt.Errorf("turn allocate: %w", err)
	}

	return &Allocation{
		Relay:       relayConn,
		RelayedAddr: relayConn.LocalAddr(),
		Selected:    server,
		client:      client,
		wire:        wireConn,
	}, nil
}

func newWireConn(transport stun.ProtoType, hostPort string) (net.PacketConn, error) {
	if transport == stun.ProtoTypeTCP {
		conn, err := net.DialTimeout("tcp", hostPort, allocTimeout)
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
