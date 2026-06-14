package obfs

import (
	"bytes"
	"testing"

	"github.com/pion/srtp/v3"
)

func TestRoundtripAes128CmHmacSha1_80(t *testing.T) {
	k := bytes.Repeat([]byte{0x42}, 16)
	s := bytes.Repeat([]byte{0x37}, 14)
	sender, err := NewSRTPSender(k, s, srtp.ProtectionProfileAes128CmHmacSha1_80)
	if err != nil {
		t.Fatal(err)
	}
	receiver, err := NewSRTPReceiver(k, s, srtp.ProtectionProfileAes128CmHmacSha1_80)
	if err != nil {
		t.Fatal(err)
	}
	pt := []byte("hello kcp tunnel")
	framed, err := sender.Frame(pt)
	if err != nil {
		t.Fatal(err)
	}
	if framed[0] != 0x80 || framed[1]&0x7f != byte(FrameData) {
		t.Fatalf("rtp header wrong: %x %x", framed[0], framed[1])
	}
	out, _, err := receiver.Parse(framed)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(out, pt) {
		t.Fatalf("roundtrip mismatch: %q", out)
	}
}

func TestRoundtripAeadAes128Gcm(t *testing.T) {
	k := bytes.Repeat([]byte{0x42}, 16)
	s := bytes.Repeat([]byte{0x37}, 12)
	sender, _ := NewSRTPSender(k, s, srtp.ProtectionProfileAeadAes128Gcm)
	receiver, _ := NewSRTPReceiver(k, s, srtp.ProtectionProfileAeadAes128Gcm)
	pt := []byte("another packet payload")
	framed, err := sender.Frame(pt)
	if err != nil {
		t.Fatal(err)
	}
	out, _, err := receiver.Parse(framed)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(out, pt) {
		t.Fatal("mismatch")
	}
}

func TestSequenceAdvances(t *testing.T) {
	k := bytes.Repeat([]byte{0x42}, 16)
	s := bytes.Repeat([]byte{0x37}, 14)
	sender, _ := NewSRTPSender(k, s, srtp.ProtectionProfileAes128CmHmacSha1_80)
	f0, _ := sender.Frame([]byte("a"))
	seq0 := uint16(f0[2])<<8 | uint16(f0[3])
	f1, _ := sender.Frame([]byte("b"))
	seq1 := uint16(f1[2])<<8 | uint16(f1[3])
	if seq1 != seq0+1 {
		t.Fatalf("seq did not advance: %d -> %d", seq0, seq1)
	}
}

func TestTamperedFails(t *testing.T) {
	k := bytes.Repeat([]byte{0x42}, 16)
	s := bytes.Repeat([]byte{0x37}, 12)
	sender, _ := NewSRTPSender(k, s, srtp.ProtectionProfileAeadAes128Gcm)
	receiver, _ := NewSRTPReceiver(k, s, srtp.ProtectionProfileAeadAes128Gcm)
	framed, _ := sender.Frame([]byte("sensitive"))
	framed[len(framed)-1] ^= 1
	if _, _, err := receiver.Parse(framed); err == nil {
		t.Fatal("expected decrypt failure")
	}
}
