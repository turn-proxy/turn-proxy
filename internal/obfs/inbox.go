package obfs

import (
	"net"
	"os"
	"time"

	"github.com/turn-proxy/turn-proxy/internal/bufpool"
)

type Inbox struct {
	ch   chan *[]byte
	done <-chan struct{}
}

func NewInbox(capacity int, done <-chan struct{}) *Inbox {
	return &Inbox{ch: make(chan *[]byte, capacity), done: done}
}

func (i *Inbox) Push(bp *[]byte) bool {
	select {
	case i.ch <- bp:
		return true
	default:
		bufpool.Put(bp)
		return false
	}
}

func (i *Inbox) Pull(b []byte, deadline time.Time) (int, error) {
	var timeout <-chan time.Time
	if !deadline.IsZero() {
		timer := time.NewTimer(time.Until(deadline))
		defer timer.Stop()
		timeout = timer.C
	}
	select {
	case bp := <-i.ch:
		n := copy(b, *bp)
		bufpool.Put(bp)
		return n, nil
	case <-i.done:
		return 0, net.ErrClosed
	case <-timeout:
		return 0, os.ErrDeadlineExceeded
	}
}
