package main

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/helsingin/goast/internal/config"
	"github.com/helsingin/goast/internal/index"
	"github.com/helsingin/goast/internal/server"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

var version = "dev"

func main() {
	server.Version = version

	cfg, err := config.Load()
	if err != nil {
		log.Fatalf("Failed to load config: %v", err)
	}

	log.Printf("goast %s — indexing %d repos...", version, len(cfg.Repos))

	start := time.Now()
	idxCfg := index.IndexConfig{
		Repos:           make([]index.RepoConfig, len(cfg.Repos)),
		ExcludePatterns: cfg.ExcludePatterns,
	}
	for i, r := range cfg.Repos {
		idxCfg.Repos[i] = index.RepoConfig{Path: r.Path}
	}

	idx, err := index.BuildIndex(idxCfg)
	if err != nil {
		log.Fatalf("Failed to build index: %v", err)
	}

	elapsed := time.Since(start)
	log.Printf("Indexed %d symbols in %d packages (%.1fs)",
		len(idx.Symbols), len(idx.Packages), elapsed.Seconds())

	holder := index.NewHolder(idx, idxCfg)
	holder.SetConfigLoader(func() (index.IndexConfig, error) {
		freshCfg, loadErr := config.Load()
		if loadErr != nil {
			return index.IndexConfig{}, loadErr
		}
		fresh := index.IndexConfig{
			Repos:           make([]index.RepoConfig, len(freshCfg.Repos)),
			ExcludePatterns: freshCfg.ExcludePatterns,
		}
		for i, r := range freshCfg.Repos {
			fresh.Repos[i] = index.RepoConfig{Path: r.Path}
		}
		return fresh, nil
	})
	srv := server.New(holder)

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	switch cfg.Transport {
	case "stdio":
		log.Printf("Starting stdio transport")
		if err := srv.Run(ctx, &mcp.StdioTransport{}); err != nil {
			fmt.Fprintf(os.Stderr, "Server error: %v\n", err)
			os.Exit(1)
		}
	case "http":
		addr := fmt.Sprintf(":%d", cfg.Port)
		handler := mcp.NewStreamableHTTPHandler(func(req *http.Request) *mcp.Server {
			return srv
		}, nil)
		log.Printf("Starting HTTP transport on %s", addr)

		httpServer := &http.Server{Addr: addr, Handler: handler}
		go func() {
			<-ctx.Done()
			httpServer.Shutdown(context.Background())
		}()
		if err := httpServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("HTTP server error: %v", err)
		}
	default:
		log.Fatalf("Unknown transport: %s", cfg.Transport)
	}
}
