package client

import (
	"context"
	"net"
	"strconv"
	"strings"
	"sync/atomic"
	"time"
)

func newResolver(servers []string) *net.Resolver {
	addrs := normalizeDNS(servers)
	if len(addrs) == 0 {
		return net.DefaultResolver
	}
	var next atomic.Uint32
	dialer := &net.Dialer{Timeout: 5 * time.Second}
	return &net.Resolver{
		PreferGo: true,
		Dial: func(ctx context.Context, network, _ string) (net.Conn, error) {
			i := int(next.Add(1)-1) % len(addrs)
			return dialer.DialContext(ctx, network, addrs[i])
		},
	}
}

func normalizeDNS(servers []string) []string {
	var out []string
	for _, s := range servers {
		s = strings.TrimSpace(s)
		if s == "" {
			continue
		}
		if _, _, err := net.SplitHostPort(s); err != nil {
			s = net.JoinHostPort(s, "53")
		}
		out = append(out, s)
	}
	return out
}

func resolveUDPAddr(ctx context.Context, resolver *net.Resolver, hostPort string) (*net.UDPAddr, error) {
	host, portStr, err := net.SplitHostPort(hostPort)
	if err != nil {
		return nil, err
	}
	port, err := strconv.Atoi(portStr)
	if err != nil {
		return nil, err
	}
	ips, err := resolver.LookupNetIP(ctx, "ip", host)
	if err != nil {
		return nil, err
	}
	ip := ips[0]
	return &net.UDPAddr{IP: ip.AsSlice(), Port: port, Zone: ip.Zone()}, nil
}
