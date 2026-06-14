package client

import (
	"context"
	"fmt"
	"log/slog"
	"net"
	"sync"
	"sync/atomic"
	"time"

	"github.com/turn-proxy/turn-broker/proto"
	"github.com/turn-proxy/turn-proxy/internal/config"
)

type streamTarget struct {
	session    *proto.Session
	serverAddr *net.UDPAddr
}

type targetResolver struct {
	resolver *net.Resolver
	broker   *brokerClient
	upstream string
	joinLink string

	cur       atomic.Pointer[streamTarget]
	ready     chan struct{}
	readyOnce sync.Once
}

func newTargetResolver(resolver *net.Resolver, broker config.BrokerSettings, upstream string) *targetResolver {
	return &targetResolver{
		resolver: resolver,
		broker:   newBroker(resolver, broker.URL),
		upstream: upstream,
		joinLink: broker.JoinLink,
		ready:    make(chan struct{}),
	}
}

func (r *targetResolver) run(ctx context.Context) {
	for {
		if t, err := r.resolve(ctx); err != nil {
			slog.Warn("resolve failed", "err", err)
		} else {
			r.publish(t)
		}
		select {
		case <-time.After(targetRefreshInterval):
		case <-ctx.Done():
			return
		}
	}
}

func (r *targetResolver) resolve(ctx context.Context) (*streamTarget, error) {
	serverAddr, err := resolveUDPAddr(ctx, r.resolver, r.upstream)
	if err != nil {
		return nil, fmt.Errorf("resolving server address %s: %w", r.upstream, err)
	}
	session, err := r.broker.Session(ctx, r.joinLink)
	if err != nil {
		return nil, fmt.Errorf("fetching broker session: %w", err)
	}
	if len(session.URLs) == 0 {
		return nil, fmt.Errorf("broker session: no turn urls advertised")
	}
	return &streamTarget{session: session, serverAddr: serverAddr}, nil
}

func (r *targetResolver) publish(t *streamTarget) {
	r.cur.Store(t)
	r.readyOnce.Do(func() { close(r.ready) })
}

func (r *targetResolver) wait(ctx context.Context) *streamTarget {
	select {
	case <-r.ready:
		return r.cur.Load()
	case <-ctx.Done():
		return nil
	}
}
