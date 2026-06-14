package client

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"time"

	"github.com/turn-proxy/turn-broker/proto"
)

type brokerClient struct {
	http *http.Client
	base string
}

func newBroker(resolver *net.Resolver, base string) *brokerClient {
	dialer := &net.Dialer{Timeout: 10 * time.Second, Resolver: resolver}
	return &brokerClient{
		http: &http.Client{
			Timeout: 15 * time.Second,
			Transport: &http.Transport{
				DialContext:           dialer.DialContext,
				ForceAttemptHTTP2:     true,
				MaxIdleConns:          10,
				IdleConnTimeout:       90 * time.Second,
				TLSHandshakeTimeout:   10 * time.Second,
				ExpectContinueTimeout: time.Second,
			},
		},
		base: base,
	}
}

func (b *brokerClient) Session(ctx context.Context, joinLink string) (*proto.Session, error) {
	u, err := url.Parse(b.base)
	if err != nil {
		return nil, fmt.Errorf("parse broker url: %w", err)
	}
	u.Path = "/v1/session"
	q := u.Query()
	q.Set("join_link", joinLink)
	u.RawQuery = q.Encode()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return nil, fmt.Errorf("new broker request: %w", err)
	}
	resp, err := b.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("GET %s: %w", u.String(), err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read body: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("broker %s returned %d: %s", u.String(), resp.StatusCode, body)
	}
	var s proto.Session
	if err := json.Unmarshal(body, &s); err != nil {
		return nil, fmt.Errorf("parse session: %w", err)
	}
	return &s, nil
}
