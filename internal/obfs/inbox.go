package obfs

import (
	"net"
	"os"

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

func (i *Inbox) Pull(b []byte, deadline <-chan struct{}) (int, error) {
	select {
	case bp := <-i.ch:
		n := copy(b, *bp)
		bufpool.Put(bp)
		return n, nil
	case <-i.done:
		return 0, net.ErrClosed
	case <-deadline:
		return 0, os.ErrDeadlineExceeded
	}
}
