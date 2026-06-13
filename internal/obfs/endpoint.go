package obfs

import (
	"context"
	"log/slog"
	"net"
	"sync/atomic"
	"time"
)

const (
	inboxBytes           = 1 << 20
	typicalPacket        = 1500
	DefaultInboxCapacity = inboxBytes / typicalPacket
)

type PacketSink interface {
	WriteTo(b []byte, addr net.Addr) (int, error)
}

type Endpoint struct {
	inbox    *Inbox
	sink     PacketSink
	cancel   context.CancelFunc
	remote   net.Addr
	local    net.Addr
	deadline atomic.Pointer[time.Time]
}

func NewEndpoint(ctx context.Context, sink PacketSink, remote, local net.Addr, capacity int) *Endpoint {
	ctx, cancel := context.WithCancel(ctx)
	return &Endpoint{
		inbox:  NewInbox(capacity, ctx.Done()),
		sink:   sink,
		cancel: cancel,
		remote: remote,
		local:  local,
	}
}

func (e *Endpoint) Deliver(bp *[]byte) {
	if !e.inbox.Push(bp) {
		slog.Debug("demux: endpoint inbox full, dropping packet")
	}
}

func (e *Endpoint) ReadFrom(b []byte) (int, net.Addr, error) {
	var deadline time.Time
	if d := e.deadline.Load(); d != nil {
		deadline = *d
	}
	n, err := e.inbox.Pull(b, deadline)
	if err != nil {
		return 0, nil, err
	}
	return n, e.remote, nil
}

func (e *Endpoint) WriteTo(b []byte, _ net.Addr) (int, error) {
	return e.sink.WriteTo(b, e.remote)
}

func (e *Endpoint) Close() error {
	e.cancel()
	return nil
}

func (e *Endpoint) LocalAddr() net.Addr { return e.local }

func (e *Endpoint) SetReadDeadline(t time.Time) error {
	e.deadline.Store(&t)
	return nil
}

func (e *Endpoint) SetDeadline(t time.Time) error      { return e.SetReadDeadline(t) }
func (e *Endpoint) SetWriteDeadline(_ time.Time) error { return nil }
