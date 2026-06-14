package obfs

import (
	"crypto/rand"
	"encoding/binary"
	"log/slog"
	"net"
	"sync"
	"sync/atomic"
	"time"

	"github.com/pion/rtp"
	"github.com/pion/srtp/v3"

	"github.com/turn-proxy/turn-proxy/internal/bufpool"
)

const (
	timestampStep  = 960
	srtpTxOverhead = 64
)

type FrameKind uint8

const (
	FrameData      FrameKind = 111
	FrameHeartbeat FrameKind = 110
)

var heartbeatPayload = []byte{0}

type SRTPSender struct {
	ctx        *srtp.Context
	ssrc       uint32
	seq        uint16
	timestamp  uint32
	marshalBuf []byte
}

func randUint32() uint32 {
	var b [4]byte
	_, _ = rand.Read(b[:])
	return binary.BigEndian.Uint32(b[:])
}

func NewSRTPSender(masterKey, masterSalt []byte, profile srtp.ProtectionProfile) (*SRTPSender, error) {
	ctx, err := srtp.CreateContext(masterKey, masterSalt, profile)
	if err != nil {
		return nil, err
	}
	return &SRTPSender{
		ctx:       ctx,
		ssrc:      randUint32(),
		seq:       uint16(randUint32()),
		timestamp: randUint32(),
	}, nil
}

func (s *SRTPSender) Frame(plaintext []byte) ([]byte, error) {
	return s.frame(nil, plaintext, FrameData)
}

func (s *SRTPSender) frame(dst, plaintext []byte, kind FrameKind) ([]byte, error) {
	pkt := rtp.Packet{
		Header: rtp.Header{
			Version:        2,
			PayloadType:    uint8(kind),
			SequenceNumber: s.seq,
			Timestamp:      s.timestamp,
			SSRC:           s.ssrc,
		},
		Payload: plaintext,
	}
	size := pkt.MarshalSize()
	if cap(s.marshalBuf) < size {
		s.marshalBuf = make([]byte, size)
	}
	n, err := pkt.MarshalTo(s.marshalBuf[:size])
	if err != nil {
		return nil, err
	}
	encrypted, err := s.ctx.EncryptRTP(dst[:0], s.marshalBuf[:n], nil)
	if err != nil {
		return nil, err
	}
	s.seq++
	s.timestamp += timestampStep
	return encrypted, nil
}

type SRTPReceiver struct {
	ctx        *srtp.Context
	decryptBuf []byte
}

func NewSRTPReceiver(masterKey, masterSalt []byte, profile srtp.ProtectionProfile) (*SRTPReceiver, error) {
	ctx, err := srtp.CreateContext(masterKey, masterSalt, profile, srtp.SRTPReplayProtection(64))
	if err != nil {
		return nil, err
	}
	return &SRTPReceiver{ctx: ctx}, nil
}

func (r *SRTPReceiver) Parse(encrypted []byte) ([]byte, FrameKind, error) {
	decrypted, err := r.ctx.DecryptRTP(r.decryptBuf[:0], encrypted, nil)
	if err != nil {
		return nil, FrameData, err
	}
	r.decryptBuf = decrypted
	var pkt rtp.Packet
	if err := pkt.Unmarshal(decrypted); err != nil {
		return nil, FrameData, err
	}
	return pkt.Payload, FrameKind(pkt.PayloadType), nil
}

type SRTPConn struct {
	inner       net.PacketConn
	remote      net.Addr
	sender      *SRTPSender
	receiver    *SRTPReceiver
	rbuf        []byte
	echo        bool
	mu          sync.Mutex
	lastInbound atomic.Int64
}

func NewSRTPConn(inner net.PacketConn, remote net.Addr, sender *SRTPSender, receiver *SRTPReceiver, echo bool) *SRTPConn {
	c := &SRTPConn{
		inner:    inner,
		remote:   remote,
		sender:   sender,
		receiver: receiver,
		rbuf:     make([]byte, MaxDatagram),
		echo:     echo,
	}
	c.lastInbound.Store(time.Now().UnixNano())
	return c
}

func (c *SRTPConn) Write(p []byte) (int, error) {
	return c.writeFrame(p, FrameData)
}

func (c *SRTPConn) WriteHeartbeat() error {
	_, err := c.writeFrame(heartbeatPayload, FrameHeartbeat)
	return err
}

func (c *SRTPConn) writeFrame(p []byte, kind FrameKind) (int, error) {
	bp := bufpool.Get(len(p) + srtpTxOverhead)
	defer bufpool.Put(bp)
	c.mu.Lock()
	framed, err := c.sender.frame((*bp)[:0], p, kind)
	c.mu.Unlock()
	if err != nil {
		return 0, err
	}
	if _, err := c.inner.WriteTo(framed, c.remote); err != nil {
		return 0, err
	}
	return len(p), nil
}

func (c *SRTPConn) Read(p []byte) (int, error) {
	for {
		n, _, err := c.inner.ReadFrom(c.rbuf)
		if err != nil {
			return 0, err
		}
		plain, kind, perr := c.receiver.Parse(c.rbuf[:n])
		if perr != nil {
			slog.Warn("srtp decrypt failed (dropping)", "err", perr)
			continue
		}
		c.lastInbound.Store(time.Now().UnixNano())
		if kind == FrameHeartbeat {
			if c.echo {
				_, _ = c.writeFrame(plain, FrameHeartbeat)
			}
			continue
		}
		nn := copy(p, plain)
		return nn, nil
	}
}

func (c *SRTPConn) LastInbound() time.Time {
	return time.Unix(0, c.lastInbound.Load())
}

func (c *SRTPConn) Close() error { return c.inner.Close() }
