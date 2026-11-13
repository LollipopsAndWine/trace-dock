package main

import (
	"context"
	"fmt"
	"github.com/trace-dock/trace-dock/internal/app"
	"os"
	"os/signal"
)

var version = "0.1.0-dev"

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	if err := app.Run(ctx, os.Args[1:], version); err != nil {
		fmt.Fprintln(os.Stderr, "trace-dock:", err)
		os.Exit(1)
	}
}
