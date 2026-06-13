package obfs

import (
	"crypto/rand"
	"encoding/binary"
	"log/slog"
	"net"

	"github.com/pion/rtp"
	"github.com/pion/srtp/v3"
)

const (
	payloadTypeOpus = 111
	timestampStep   = 960
)

type SRTPSender struct {
	ctx        *srtp.Context
	ssrc       uint32
	seq        uint16
	timestamp  uint32
	marshalBuf []byte
	encryptBuf []byte
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
	pkt := rtp.Packet{
		Header: rtp.Header{
			Version:        2,
			PayloadType:    payloadTypeOpus,
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
	encrypted, err := s.ctx.EncryptRTP(s.encryptBuf[:0], s.marshalBuf[:n], nil)
	if err != nil {
		return nil, err
	}
	s.encryptBuf = encrypted
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

func (r *SRTPReceiver) Parse(encrypted []byte) ([]byte, error) {
	decrypted, err := r.ctx.DecryptRTP(r.decryptBuf[:0], encrypted, nil)
	if err != nil {
		return nil, err
	}
	r.decryptBuf = decrypted
	var pkt rtp.Packet
	if err := pkt.Unmarshal(decrypted); err != nil {
		return nil, err
	}
	return pkt.Payload, nil
}

type SRTPConn struct {
	inner    net.PacketConn
	remote   net.Addr
	sender   *SRTPSender
	receiver *SRTPReceiver
	rbuf     []byte
}

func NewSRTPConn(inner net.PacketConn, remote net.Addr, sender *SRTPSender, receiver *SRTPReceiver) *SRTPConn {
	return &SRTPConn{
		inner:    inner,
		remote:   remote,
		sender:   sender,
		receiver: receiver,
		rbuf:     make([]byte, MaxDatagram),
	}
}

func (c *SRTPConn) Write(p []byte) (int, error) {
	framed, err := c.sender.Frame(p)
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
		plain, perr := c.receiver.Parse(c.rbuf[:n])
		if perr != nil {
			slog.Warn("srtp decrypt failed (dropping)", "err", perr)
			continue
		}
		nn := copy(p, plain)
		return nn, nil
	}
}

func (c *SRTPConn) Close() error { return c.inner.Close() }
