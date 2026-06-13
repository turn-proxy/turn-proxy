package config

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net"
	"net/url"
	"os"

	"github.com/pion/stun/v3"
)

type Config struct {
	Listen   string         `json:"listen"`
	Upstream string         `json:"upstream"`
	Broker   BrokerSettings `json:"broker"`
	Turn     TurnSettings   `json:"turn"`
}

type BrokerSettings struct {
	URL      string `json:"url"`
	JoinLink string `json:"join_link"`
}

const MaxStreams = 64

type TurnSettings struct {
	Transport           string `json:"transport"`
	Streams             int    `json:"streams"`
	PeerIdleTimeoutSecs uint64 `json:"peer_idle_timeout_secs"`
}

func defaults() Config {
	return Config{
		Turn: TurnSettings{
			Transport:           "tcp",
			Streams:             10,
			PeerIdleTimeoutSecs: 60,
		},
	}
}

func Load(path string) (Config, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return Config{}, fmt.Errorf("reading config: %w", err)
	}
	return Parse(raw)
}

func Parse(raw []byte) (Config, error) {
	cfg := defaults()
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&cfg); err != nil {
		return Config{}, fmt.Errorf("parsing config: %w", err)
	}
	if err := cfg.validate(); err != nil {
		return Config{}, err
	}
	return cfg, nil
}

func (c *Config) validate() error {
	if _, _, err := net.SplitHostPort(c.Listen); err != nil {
		return fmt.Errorf("listen %q: %w", c.Listen, err)
	}
	if c.Upstream == "" {
		return fmt.Errorf("upstream is required")
	}
	if err := validateURL(c.Broker.URL); err != nil {
		return fmt.Errorf("broker.url %q: %w", c.Broker.URL, err)
	}
	if err := validateURL(c.Broker.JoinLink); err != nil {
		return fmt.Errorf("broker.join_link %q: %w", c.Broker.JoinLink, err)
	}
	if stun.NewProtoType(c.Turn.Transport) == stun.ProtoTypeUnknown {
		return fmt.Errorf("turn.transport %q must be tcp or udp", c.Turn.Transport)
	}
	if c.Turn.Streams < 1 || c.Turn.Streams > MaxStreams {
		return fmt.Errorf("turn.streams must be between 1 and %d, got %d", MaxStreams, c.Turn.Streams)
	}
	return nil
}

func validateURL(s string) error {
	u, err := url.Parse(s)
	if err != nil {
		return err
	}
	if u.Scheme == "" || u.Host == "" {
		return fmt.Errorf("must be an absolute url with scheme and host")
	}
	return nil
}

func (c *Config) Transport() stun.ProtoType {
	return stun.NewProtoType(c.Turn.Transport)
}
