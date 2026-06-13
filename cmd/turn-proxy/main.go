package main

import (
	"cmp"
	"context"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/turn-proxy/turn-proxy/internal/client"
	"github.com/turn-proxy/turn-proxy/internal/config"
	"github.com/turn-proxy/turn-proxy/internal/server"
)

func main() {
	cfgPath := flag.String("config", cmp.Or(os.Getenv("TURN_PROXY_CONFIG"), "turn-proxy.json"), "config path")
	flag.Parse()

	level := slog.LevelInfo
	if os.Getenv("DEBUG") != "" {
		level = slog.LevelDebug
	}
	slog.SetDefault(slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: level})))

	cmd := "run"
	if flag.NArg() > 0 {
		cmd = flag.Arg(0)
	}

	cfg, err := config.Load(*cfgPath)
	if err != nil {
		slog.Error("loading config", "path", *cfgPath, "err", err)
		os.Exit(1)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	switch cmd {
	case "validate":
		slog.Info("config OK", "listen", cfg.Listen, "upstream", cfg.Upstream)
	case "run":
		err = client.Run(ctx, cfg)
	case "serve":
		err = server.Serve(ctx, cfg)
	default:
		err = fmt.Errorf("unknown command %q (want run|serve|validate)", cmd)
	}
	if err != nil {
		slog.Error("exit", "err", err)
		os.Exit(1)
	}
}
