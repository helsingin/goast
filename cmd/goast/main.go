package main

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
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
	idxCfg := makeIndexConfig(cfg)

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
		return makeIndexConfig(freshCfg), nil
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
			if err := httpServer.Shutdown(context.Background()); err != nil {
				log.Printf("HTTP shutdown error: %v", err)
			}
		}()
		if err := httpServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("HTTP server error: %v", err)
		}
	default:
		log.Fatalf("Unknown transport: %s", cfg.Transport)
	}
}

func makeIndexConfig(cfg *config.Config) index.IndexConfig {
	result := index.IndexConfig{
		Repos:           make([]index.RepoConfig, len(cfg.Repos)),
		ExcludePatterns: cfg.ExcludePatterns,
		BuildContexts:   make([]index.BuildContext, len(cfg.BuildContexts)),
	}
	for i, repo := range cfg.Repos {
		name := repo.Name
		if name == "" {
			name = filepath.Base(filepath.Clean(repo.Path))
		}
		result.Repos[i] = index.RepoConfig{
			Name:                  name,
			Path:                  repo.Path,
			IncludeTests:          repo.TestsEnabled(cfg.IncludeTests),
			TypedMethodReferences: repo.TypedReferencesEnabled(cfg.TypedMethodReferences),
		}
	}
	for i, buildContext := range cfg.BuildContexts {
		result.BuildContexts[i] = index.BuildContext{
			GOOS:       buildContext.GOOS,
			GOARCH:     buildContext.GOARCH,
			CGOEnabled: buildContext.CGOEnabled,
			BuildTags:  buildContext.BuildTags,
			ToolTags:   buildContext.ToolTags,
		}
	}
	return result
}
