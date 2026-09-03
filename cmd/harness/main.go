package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"harness/internal/agent"
	"harness/internal/config"
	"harness/internal/events"
	"harness/internal/probe"
	"harness/internal/session"
	"harness/internal/tools"
	webserver "harness/internal/web"
)

func main() {
	configPath := flag.String("config", "harness.json", "configuration file")
	flag.Parse()
	cfg, migrated, err := config.Load(*configPath)
	if err != nil {
		log.Fatal(err)
	}
	if migrated {
		log.Printf("migrated %s: server → servers[local]", filepath.Base(*configPath))
	}
	writers, err := events.NewWriters(cfg.LogDir)
	if err != nil {
		log.Fatal(err)
	}
	defer writers.Close()
	bus := events.NewBus()
	bus.SetSink(writers.Write)
	profile := &cfg.Servers[0]
	caps, findings, err := probe.Probe(context.Background(), profile)
	if err != nil {
		bus.Publish(events.New(events.Error, "", "", map[string]any{"where": "probe", "message": err.Error()}))
		log.Printf("probe local: %v", err)
	} else {
		profile.Capabilities = caps
		profile.Reasoning.ValidEfforts = append([]string(nil), caps.ValidEfforts...)
		if err := cfg.Save(*configPath); err != nil {
			log.Fatal(err)
		}
		bus.Publish(events.New(events.ServerProbed, "", "", map[string]any{"server_id": profile.ID, "capabilities": caps, "findings": findings}))
	}
	web := webserver.New(cfg, *configPath, "web", bus)
	registry := session.NewRegistry(bus, writers, web.Profile, cfg.Run.MaxTurns)
	web.SetRegistry(registry)
	renderer, err := agent.LoadTemplate(filepath.Join("prompts", "system.md"))
	if err != nil {
		log.Fatal(err)
	}
	workspaces := session.NewWorkspaceRegistry()
	coordinator := tools.NewFileCoordinator(workspaces, registry.Label, bus)
	toolRegistry := tools.New(
		tools.NewReadFile(cfg.Tools.ReadFile),
		tools.NewListDir(cfg.Tools.ListDir),
		tools.NewWriteFile(coordinator),
		tools.NewEditFile(coordinator),
		tools.NewGrep(cfg.Tools.Grep, cfg.Tools.ListDir),
		tools.NewShell(cfg.Shell),
	)
	runner := agent.NewRunner(bus, toolRegistry, renderer, web.Profile, web.ConfigSnapshot)
	scheduler := agent.NewScheduler(runner, registry, bus, web.ConfigSnapshot)
	web.SetRuntime(scheduler, runner, renderer)
	if _, err := registry.Create("main", cfg.Servers[0].ID, cfg.Workspace); err != nil {
		log.Fatal(err)
	}
	httpServer := &http.Server{Addr: cfg.Listen, Handler: web.Handler(), ReadHeaderTimeout: 10 * time.Second}
	errors := make(chan error, 1)
	go func() { log.Printf("AgentB listening on http://%s", cfg.Listen); errors <- httpServer.ListenAndServe() }()
	signals := make(chan os.Signal, 1)
	signal.Notify(signals, os.Interrupt, syscall.SIGTERM)
	select {
	case sig := <-signals:
		log.Printf("stopping on %s", sig)
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_ = httpServer.Shutdown(ctx)
	case err := <-errors:
		if err != nil && err != http.ErrServerClosed {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
	}
}
