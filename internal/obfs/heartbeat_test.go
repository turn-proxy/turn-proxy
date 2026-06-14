package obfs

import (
	"bytes"
	"net"
	"sync"
	"testing"
	"time"

	"github.com/pion/srtp/v3"
)

type pipeConn struct {
	readCh  chan []byte
	writeCh chan []byte
	addr    net.Addr
	done    chan struct{}
}

func (p *pipeConn) ReadFrom(b []byte) (int, net.Addr, error) {
	select {
	case pkt := <-p.readCh:
		return copy(b, pkt), p.addr, nil
	case <-p.done:
		return 0, nil, net.ErrClosed
	}
}

func (p *pipeConn) WriteTo(b []byte, _ net.Addr) (int, error) {
	cp := make([]byte, len(b))
	copy(cp, b)
	select {
	case p.writeCh <- cp:
		return len(b), nil
	case <-p.done:
		return 0, net.ErrClosed
	}
}

func (p *pipeConn) Close() error                     { return nil }
func (p *pipeConn) LocalAddr() net.Addr              { return p.addr }
func (p *pipeConn) SetDeadline(time.Time) error      { return nil }
func (p *pipeConn) SetReadDeadline(time.Time) error  { return nil }
func (p *pipeConn) SetWriteDeadline(time.Time) error { return nil }

func TestHeartbeatEchoUpdatesLastInbound(t *testing.T) {
	profile := srtp.ProtectionProfileAeadAes128Gcm
	ka := bytes.Repeat([]byte{0x11}, 16)
	sa := bytes.Repeat([]byte{0x22}, 12)
	kb := bytes.Repeat([]byte{0x33}, 16)
	sb := bytes.Repeat([]byte{0x44}, 12)

	clientSender, _ := NewSRTPSender(ka, sa, profile)
	serverReceiver, _ := NewSRTPReceiver(ka, sa, profile)
	serverSender, _ := NewSRTPSender(kb, sb, profile)
	clientReceiver, _ := NewSRTPReceiver(kb, sb, profile)

	clientAddr := &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 1}
	serverAddr := &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 2}
	c2s := make(chan []byte, 8)
	s2c := make(chan []byte, 8)
	done := make(chan struct{})
	defer close(done)

	clientPipe := &pipeConn{readCh: s2c, writeCh: c2s, addr: serverAddr, done: done}
	serverPipe := &pipeConn{readCh: c2s, writeCh: s2c, addr: clientAddr, done: done}

	clientConn := NewSRTPConn(clientPipe, serverAddr, clientSender, clientReceiver, false)
	serverConn := NewSRTPConn(serverPipe, clientAddr, serverSender, serverReceiver, true)

	serverData := make(chan []byte, 4)
	clientData := make(chan []byte, 4)
	go readLoop(serverConn, serverData, done)
	go readLoop(clientConn, clientData, done)

	before := clientConn.LastInbound()
	time.Sleep(2 * time.Millisecond)
	if err := clientConn.WriteHeartbeat(); err != nil {
		t.Fatalf("write heartbeat: %v", err)
	}

	deadline := time.After(2 * time.Second)
	for !clientConn.LastInbound().After(before) {
		select {
		case <-deadline:
			t.Fatal("client LastInbound did not advance after heartbeat echo")
		case d := <-clientData:
			t.Fatalf("heartbeat surfaced as data on client: %q", d)
		case <-time.After(5 * time.Millisecond):
		}
	}

	if _, err := clientConn.Write([]byte("payload")); err != nil {
		t.Fatalf("write data: %v", err)
	}
	select {
	case got := <-serverData:
		if !bytes.Equal(got, []byte("payload")) {
			t.Fatalf("server data mismatch: %q", got)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("data packet did not reach server")
	}
}

func readLoop(c *SRTPConn, out chan<- []byte, done <-chan struct{}) {
	buf := make([]byte, MaxDatagram)
	for {
		n, err := c.Read(buf)
		if err != nil {
			return
		}
		cp := make([]byte, n)
		copy(cp, buf[:n])
		select {
		case out <- cp:
		case <-done:
			return
		}
	}
}

func TestConcurrentSendersNoRace(t *testing.T) {
	profile := srtp.ProtectionProfileAeadAes128Gcm
	ka := bytes.Repeat([]byte{0x55}, 16)
	sa := bytes.Repeat([]byte{0x66}, 12)
	kb := bytes.Repeat([]byte{0x77}, 16)
	sb := bytes.Repeat([]byte{0x88}, 12)

	clientSender, _ := NewSRTPSender(ka, sa, profile)
	serverReceiver, _ := NewSRTPReceiver(ka, sa, profile)
	serverSender, _ := NewSRTPSender(kb, sb, profile)
	clientReceiver, _ := NewSRTPReceiver(kb, sb, profile)

	clientAddr := &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 1}
	serverAddr := &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 2}
	c2s := make(chan []byte, 64)
	s2c := make(chan []byte, 64)
	done := make(chan struct{})
	defer close(done)

	clientPipe := &pipeConn{readCh: s2c, writeCh: c2s, addr: serverAddr, done: done}
	serverPipe := &pipeConn{readCh: c2s, writeCh: s2c, addr: clientAddr, done: done}
	clientConn := NewSRTPConn(clientPipe, serverAddr, clientSender, clientReceiver, false)
	serverConn := NewSRTPConn(serverPipe, clientAddr, serverSender, serverReceiver, true)

	go drainRead(serverConn, done)
	go drainRead(clientConn, done)

	const n = 500
	var wg sync.WaitGroup
	wg.Go(func() {
		for range n {
			if _, err := clientConn.Write([]byte("payload")); err != nil {
				return
			}
		}
	})
	wg.Go(func() {
		for range n {
			if err := clientConn.WriteHeartbeat(); err != nil {
				return
			}
		}
	})
	wg.Wait()
}

func drainRead(c *SRTPConn, done <-chan struct{}) {
	buf := make([]byte, MaxDatagram)
	for {
		select {
		case <-done:
			return
		default:
		}
		if _, err := c.Read(buf); err != nil {
			return
		}
	}
}
