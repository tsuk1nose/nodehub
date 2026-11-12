package main

import (
	"bufio"
	"context"
	"flag"
	"fmt"
	"golang.org/x/crypto/bcrypt"
	"log/slog"
	"net/http"
	"nodehub/internal/hub"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"
)

func main() {
	config := flag.String("config", "config/config.json", "configuration file")
	hash := flag.Bool("hash-password", false, "read password from stdin and print bcrypt hash")
	backup := flag.String("backup", "", "create consistent SQLite backup and exit")
	flag.Parse()
	if *hash {
		line, e := bufio.NewReader(os.Stdin).ReadString('\n')
		if e != nil && line == "" {
			die("cannot read password")
		}
		pw := strings.TrimSuffix(strings.TrimSuffix(line, "\n"), "\r")
		if len(pw) < 12 || len(pw) > 72 {
			die("password must be 12..72 bytes")
		}
		b, e := bcrypt.GenerateFromPassword([]byte(pw), bcrypt.DefaultCost)
		if e != nil {
			die("password hashing failed")
		}
		fmt.Println(string(b))
		return
	}
	c, e := hub.LoadConfig(*config)
	if e != nil {
		die(e.Error())
	}
	s, e := hub.OpenStore(c.Database)
	if e != nil {
		die("cannot open database")
	}
	defer s.Close()
	if *backup != "" {
		if e = s.Backup(*backup); e != nil {
			die(e.Error())
		}
		return
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	targets, e := c.Terminal.Targets()
	if e != nil {
		die(e.Error())
	}
	a := hub.NewServerWithTargets(c, s, targets)
	defer a.Close()
	go a.RunSync(ctx)
	srv := &http.Server{Addr: c.Listen, Handler: a, ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 30 * time.Second, WriteTimeout: 45 * time.Second, IdleTimeout: 60 * time.Second, MaxHeaderBytes: 16 << 10}
	go func() {
		<-ctx.Done()
		shutdown, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_ = srv.Shutdown(shutdown)
	}()
	slog.Info("nodehub listening", "address", c.Listen)
	if e = srv.ListenAndServe(); e != nil && e != http.ErrServerClosed {
		die("HTTP server failed")
	}
}
func die(msg string) { slog.Error(msg); os.Exit(1) }
