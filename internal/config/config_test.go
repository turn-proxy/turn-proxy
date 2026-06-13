package config

import "testing"

const broker = `"broker": {
	"url": "http://127.0.0.1:8787",
	"join_link": "https://vk.com/call/join/abc"
}`

func TestParsesMinimalConfig(t *testing.T) {
	raw := `{"listen":"127.0.0.1:1080","upstream":"127.0.0.1:9999",` + broker + `}`
	cfg, err := Parse([]byte(raw))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if cfg.Upstream != "127.0.0.1:9999" {
		t.Errorf("upstream = %q", cfg.Upstream)
	}
	if cfg.Turn.Transport != "tcp" || cfg.Turn.Streams != 10 || cfg.Turn.PeerIdleTimeoutSecs != 60 {
		t.Errorf("defaults wrong: %+v", cfg.Turn)
	}
}

func TestPartialTurnFillsDefaults(t *testing.T) {
	raw := `{"listen":"127.0.0.1:1080","upstream":"127.0.0.1:9999","turn":{"streams":3},` + broker + `}`
	cfg, err := Parse([]byte(raw))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if cfg.Turn.Streams != 3 || cfg.Turn.Transport != "tcp" || cfg.Turn.PeerIdleTimeoutSecs != 60 {
		t.Errorf("partial turn wrong: %+v", cfg.Turn)
	}
}

func TestRejectsStaleTopLevelKey(t *testing.T) {
	raw := `{"listen":"127.0.0.1:1080","upstream":"127.0.0.1:9999","turn_transport":"udp",` + broker + `}`
	if _, err := Parse([]byte(raw)); err == nil {
		t.Fatal("expected error for unknown key")
	}
}

func TestRejectsServerAllocations(t *testing.T) {
	raw := `{"listen":"127.0.0.1:1080","upstream":"127.0.0.1:9999","turn":{"server_allocations":2},` + broker + `}`
	if _, err := Parse([]byte(raw)); err == nil {
		t.Fatal("expected error for removed server_allocations key")
	}
}

func TestRejectsOutOfRangeStreams(t *testing.T) {
	for _, streams := range []string{"0", "-1", "65", "1000"} {
		raw := `{"listen":"127.0.0.1:1080","upstream":"127.0.0.1:9999","turn":{"streams":` + streams + `},` + broker + `}`
		if _, err := Parse([]byte(raw)); err == nil {
			t.Errorf("expected error for streams=%s", streams)
		}
	}
	for _, streams := range []string{"1", "64"} {
		raw := `{"listen":"127.0.0.1:1080","upstream":"127.0.0.1:9999","turn":{"streams":` + streams + `},` + broker + `}`
		if _, err := Parse([]byte(raw)); err != nil {
			t.Errorf("streams=%s should be valid: %v", streams, err)
		}
	}
}

func TestMissingRequiredFields(t *testing.T) {
	for _, raw := range []string{
		`{"upstream":"127.0.0.1:9999"}`,
		`{"listen":"127.0.0.1:1080"}`,
		`{"listen":"127.0.0.1:1080","upstream":"127.0.0.1:9999"}`,
	} {
		if _, err := Parse([]byte(raw)); err == nil {
			t.Errorf("expected error for %q", raw)
		}
	}
}
