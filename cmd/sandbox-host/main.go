package main

import (
	"context"
	"flag"
	"log/slog"
	"os/signal"
	"syscall"
	"time"
)

func main() {
	addr := flag.String("listen", ":8080", "listen address")
	flag.Parse()

	logger := slog.Default()
	logger.Info("sandbox-host skeleton starting", "listen", *addr)

	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	<-ctx.Done()
	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer shutdownCancel()
	<-shutdownCtx.Done()
	logger.Info("sandbox-host skeleton shutdown complete")
}
