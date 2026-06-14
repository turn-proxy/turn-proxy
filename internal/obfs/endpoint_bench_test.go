package obfs

import (
	"bytes"
	"testing"

	"github.com/turn-proxy/turn-proxy/internal/bufpool"
)

func BenchmarkEndpointRoundtrip(b *testing.B) {
	endpoint := NewEndpoint(b.Context(), nil, nil, nil, DefaultInboxCapacity)
	payload := bytes.Repeat([]byte{0xab}, 1200)
	out := make([]byte, 2048)
	b.ReportAllocs()
	b.SetBytes(int64(len(payload)))
	for b.Loop() {
		bp := bufpool.Get(len(payload))
		copy(*bp, payload)
		endpoint.Deliver(bp)
		if _, _, err := endpoint.ReadFrom(out); err != nil {
			b.Fatal(err)
		}
	}
}
