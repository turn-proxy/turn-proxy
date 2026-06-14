package client

import (
	"net"
	"slices"
	"testing"
)

func TestNormalizeDNS(t *testing.T) {
	got := normalizeDNS([]string{" 8.8.8.8 ", "", "1.1.1.1:5353", "\t"})
	want := []string{"8.8.8.8:53", "1.1.1.1:5353"}
	if !slices.Equal(got, want) {
		t.Fatalf("normalizeDNS = %v, want %v", got, want)
	}
	if normalizeDNS(nil) != nil {
		t.Fatalf("normalizeDNS(nil) should be empty")
	}
}

func TestNewResolverEmptyIsDefault(t *testing.T) {
	if newResolver(nil) != net.DefaultResolver {
		t.Fatal("empty servers should yield net.DefaultResolver, not a custom one")
	}
	if newResolver([]string{"9.9.9.9"}) == net.DefaultResolver {
		t.Fatal("configured servers should yield a scoped resolver")
	}
}

func TestResolveUDPAddrIPLiteral(t *testing.T) {
	addr, err := resolveUDPAddr(t.Context(), net.DefaultResolver, "203.0.113.7:51820")
	if err != nil {
		t.Fatalf("resolveUDPAddr: %v", err)
	}
	if !addr.IP.Equal(net.ParseIP("203.0.113.7")) || addr.Port != 51820 {
		t.Fatalf("resolveUDPAddr = %v, want 203.0.113.7:51820", addr)
	}
}
