package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"

	"pscaper/internal/app"
)

var version = "1.5.2"

func main() {
	cmd := "serve"
	if len(os.Args) > 1 {
		cmd = os.Args[1]
	}
	switch cmd {
	case "version", "--version", "-v":
		fmt.Println("pscaper", version)
	case "tui":
		api := os.Getenv("PSCAPER_API_ADDR")
		if api == "" {
			api = "127.0.0.1:8788"
		}
		if err := app.RunTUI(api); err != nil {
			log.Fatal(err)
		}
	case "serve":
		serve()
	default:
		fmt.Fprintf(os.Stderr, "usage: pscaper [serve|tui|version]\n")
		os.Exit(2)
	}
}
func serve() {
	cfg, err := app.LoadConfig()
	if err != nil {
		log.Fatal(err)
	}
	if err := os.MkdirAll(cfg.DataDir, 0o755); err != nil {
		log.Fatal(err)
	}
	store, err := app.OpenStore(filepath.Join(cfg.DataDir, "proxies.snapshot.json.gz"))
	if err != nil {
		log.Fatal(err)
	}
	defer store.Close()
	eng := app.NewEngine(cfg, store)
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	log.Printf("pscaper %s starting: workers=%d api=%s probe=%s:%d/%d", version, cfg.Workers, cfg.APIAddr, cfg.ProbeHost, cfg.ProbeHTTPPort, cfg.ProbeHTTPSPort)
	if err := eng.Run(ctx); err != nil && err != context.Canceled {
		log.Fatal(err)
	}
}
