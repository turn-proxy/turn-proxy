package client

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"time"

	"github.com/turn-proxy/turn-broker/proto"
)

var brokerClient = &http.Client{Timeout: 15 * time.Second}

func getSession(ctx context.Context, base, joinLink string) (*proto.Session, error) {
	u, err := url.Parse(base)
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
	resp, err := brokerClient.Do(req)
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
