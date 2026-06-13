package obfs

import (
	"bytes"
	"testing"

	"github.com/pion/srtp/v3"
)

func BenchmarkFrame(b *testing.B) {
	k := bytes.Repeat([]byte{0x42}, 16)
	s := bytes.Repeat([]byte{0x37}, 12)
	sender, _ := NewSRTPSender(k, s, srtp.ProtectionProfileAeadAes128Gcm)
	pt := bytes.Repeat([]byte{0xab}, 1200)
	b.ReportAllocs()
	b.SetBytes(int64(len(pt)))
	for b.Loop() {
		if _, err := sender.Frame(pt); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkParse(b *testing.B) {
	k := bytes.Repeat([]byte{0x42}, 16)
	s := bytes.Repeat([]byte{0x37}, 12)
	sender, _ := NewSRTPSender(k, s, srtp.ProtectionProfileAeadAes128Gcm)
	receiver, _ := NewSRTPReceiver(k, s, srtp.ProtectionProfileAeadAes128Gcm)
	pt := bytes.Repeat([]byte{0xab}, 1200)
	b.ReportAllocs()
	b.SetBytes(int64(len(pt)))
	for b.Loop() {
		framed, _ := sender.Frame(pt)
		if _, err := receiver.Parse(framed); err != nil {
			b.Fatal(err)
		}
	}
}
