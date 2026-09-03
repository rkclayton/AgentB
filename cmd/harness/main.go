package main

import (
	"bufio"
	"context"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"harness/internal/agent"
	"harness/internal/config"
	"harness/internal/credential"
	"harness/internal/events"
	"harness/internal/llm"
	"harness/internal/memory"
	"harness/internal/probe"
	"harness/internal/serviceaccount"
	"harness/internal/session"
	"harness/internal/tools"
	webserver "harness/internal/web"
)

func main() {
	configPath := flag.String("config", "harness.json", "configuration file")
	replayPaths := flag.String("replay", "", "comma-separated session JSONL files to replay")
	flag.Parse()
	cfg, migrated, created, err := config.Load(*configPath)
	if err != nil {
		log.Fatal(err)
	}
	if created {
		log.Printf("created %s from harness.example.json - set servers[0].base_url and model", filepath.Base(*configPath))
	}
	if migrated {
		log.Printf("migrated %s: server → servers[local]", filepath.Base(*configPath))
	}
	facts := readServingFacts("SERVING.md")
	if !facts.Complete {
		log.Printf("debug: SERVING.md missing or partial; skipping /tokenize latency hint")
	} else if facts.TokenizeBlocksOnSlot == "yes" {
		log.Printf("tokenize blocks on the generation slot (%d ms measured busy); context.accounting: \"estimated\" avoids it", facts.TokenizeBusyMS)
	}
	if strings.TrimSpace(*replayPaths) != "" {
		replay, loadErr := events.LoadReplay(strings.Split(*replayPaths, ","))
		if loadErr != nil {
			log.Fatal(loadErr)
		}
		web := webserver.New(cfg, *configPath, "web", events.NewBus())
		web.SetReplay(replay)
		if err := serve(cfg, web.Handler()); err != nil {
			log.Fatal(err)
		}
		return
	}
	writers, err := events.NewWriters(cfg.LogDir)
	if err != nil {
		log.Fatal(err)
	}
	defer writers.Close()
	bus := events.NewBus()
	bus.SetSink(writers.Write)
	profile := &cfg.Servers[0]
	if reason := config.ProfileSetupReason(profile); reason != "" {
		log.Printf("probe %s skipped: %s", profile.ID, reason)
	} else {
		caps, findings, probeErr := probe.Probe(context.Background(), profile)
		if probeErr != nil {
			bus.Publish(events.New(events.Error, "", "", map[string]any{"where": "probe", "message": probeErr.Error()}))
			log.Printf("probe %s: %v", profile.ID, probeErr)
		} else {
			profile.Capabilities = caps
			profile.Reasoning.ValidEfforts = append([]string(nil), caps.ValidEfforts...)
			if err := cfg.Save(*configPath); err != nil {
				log.Fatal(err)
			}
			bus.Publish(events.New(events.ServerProbed, "", "", map[string]any{"server_id": profile.ID, "capabilities": caps, "findings": findings}))
		}
	}
	web := webserver.New(cfg, *configPath, "web", bus)
	memoryManager := memory.New(filepath.Dir(*configPath), web.ConfigSnapshot, func(ctx context.Context, serverID, text string) (int, error) {
		profile, ok := web.Profile(serverID)
		if !ok || !profile.Capabilities.Tokenize {
			return 0, fmt.Errorf("tokenizer unavailable")
		}
		return llm.New(profile).Tokenize(ctx, text, false)
	})
	registry := session.NewRegistry(bus, writers, web.Profile, cfg.Run.MaxTurns, web.ConfigSnapshot)
	registry.SetMemoryLoader(memoryManager.Load)
	web.SetRegistry(registry)
	renderer, err := agent.LoadTemplate(filepath.Join("prompts", "system.md"))
	if err != nil {
		log.Fatal(err)
	}
	workspaces := session.NewWorkspaceRegistry()
	coordinator := tools.NewFileCoordinator(workspaces, registry.Label, bus)
	credentialStore := credential.New(*configPath)
	shellTool := tools.NewShell(cfg.Shell)
	shellTool.Configure(*cfg)
	shellTool.SetCredentialStore(credentialStore)
	shellTool.SetIdentityReporter(func(status tools.ShellIdentityStatus) {
		bus.Publish(events.New(events.ShellIdentity, "", "", status))
	})
	web.SetShellSecurity(credentialStore, shellTool)
	web.SetServiceAccountManager(serviceaccount.New(filepath.Join("scripts", "setup-service-account.ps1")))
	toolRegistry := tools.New(
		tools.NewReadFile(cfg.Tools.ReadFile),
		tools.NewListDir(cfg.Tools.ListDir),
		tools.NewWriteFile(coordinator),
		tools.NewEditFile(coordinator),
		tools.NewGrep(cfg.Tools.Grep, cfg.Tools.ListDir),
		shellTool,
		tools.NewRemember(memoryManager, bus),
		tools.NewGlob(),
	)
	runner := agent.NewRunner(bus, toolRegistry, renderer, web.Profile, web.ConfigSnapshot)
	scheduler := agent.NewScheduler(runner, registry, bus, web.ConfigSnapshot)
	web.SetRuntime(scheduler, runner, renderer)
	mainSession, err := registry.Create("main", cfg.Servers[0].ID, cfg.Workspace)
	if err != nil {
		log.Fatal(err)
	}
	runner.PublishBudget(context.Background(), mainSession)
	if err := serve(cfg, web.Handler()); err != nil {
		log.Fatal(err)
	}
}

func serve(cfg *config.Config, handler http.Handler) error {
	httpServer := &http.Server{Addr: cfg.Listen, Handler: handler, ReadHeaderTimeout: 10 * time.Second}
	errors := make(chan error, 1)
	go func() { log.Printf("AgentB listening on http://%s", cfg.Listen); errors <- httpServer.ListenAndServe() }()
	signals := make(chan os.Signal, 1)
	signal.Notify(signals, os.Interrupt, syscall.SIGTERM)
	select {
	case sig := <-signals:
		log.Printf("stopping on %s", sig)
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		return httpServer.Shutdown(ctx)
	case err := <-errors:
		if err != nil && err != http.ErrServerClosed {
			return err
		}
		return nil
	}
}

type servingFacts struct {
	TokenizeIdleMS       int
	TokenizeBusyMS       int
	TokenizeBlocksOnSlot string
	Complete             bool
}

// readServingFacts reads only the accounting facts needed at startup. Missing or
// malformed values remain zero-valued so a documentation issue cannot stop AgentB.
func readServingFacts(path string) servingFacts {
	file, err := os.Open(path)
	if err != nil {
		return servingFacts{}
	}
	defer file.Close()

	var facts servingFacts
	var idleFound, busyFound, blocksFound bool
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		key, value, found := strings.Cut(strings.TrimSpace(scanner.Text()), "=")
		if !found {
			continue
		}
		switch key {
		case "tokenize_idle_ms":
			facts.TokenizeIdleMS, err = strconv.Atoi(strings.TrimSpace(value))
			idleFound = err == nil
		case "tokenize_busy_ms":
			facts.TokenizeBusyMS, err = strconv.Atoi(strings.TrimSpace(value))
			busyFound = err == nil
		case "tokenize_blocks_on_slot":
			facts.TokenizeBlocksOnSlot = strings.ToLower(strings.TrimSpace(value))
			blocksFound = facts.TokenizeBlocksOnSlot == "yes" || facts.TokenizeBlocksOnSlot == "no" || facts.TokenizeBlocksOnSlot == "partial"
		}
	}
	facts.Complete = scanner.Err() == nil && idleFound && busyFound && blocksFound
	return facts
}
