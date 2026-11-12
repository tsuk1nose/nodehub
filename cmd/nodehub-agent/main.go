package main

import (
	"context"
	"flag"
	"log/slog"
	"nodehub/internal/edge"
	"os"
	"os/signal"
	"syscall"
)

func main() {
	config := flag.String("config", "/etc/nodehub-agent/config.json", "agent config")
	enroll := flag.Bool("enroll", false, "bind a locally generated Ed25519 key using an existing node credential")
	bootstrap := flag.String("bootstrap-state", "/var/lib/nodehub-init/state.json", "existing node registration state (enrollment only)")
	flag.Parse()
	c, e := edge.LoadConfig(*config)
	if e != nil {
		fail("invalid agent configuration")
	}
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	if *enroll {
		if e = edge.Enroll(ctx, c, *bootstrap); e != nil {
			fail(e.Error())
		}
		slog.Info("agent identity enrolled")
		return
	}
	a, e := edge.New(c)
	if e != nil {
		fail(e.Error())
	}
	if e = a.Run(ctx); e != nil {
		fail("agent stopped")
	}
}
func fail(s string) { slog.Error(s); os.Exit(1) }
