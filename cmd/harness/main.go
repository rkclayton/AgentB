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
	"harness/internal/hardening"
	"harness/internal/llm"
	"harness/internal/memory"
	"harness/internal/serviceaccount"
	"harness/internal/session"
	"harness/internal/tools"
	webserver "harness/internal/web"
)

func main() {
	if err := startupElevationError(processIsElevated()); err != nil {
		log.Fatal(err)
	}
	configOverride := flag.String("config", "", "configuration file (overrides AGENTB_CONFIG and installed/default locations)")
	applicationOverride := flag.String("app-root", "", "application root containing web, prompts, scripts, and harness.example.json")
	dataOverride := flag.String("data-root", "", "operator data root containing configuration, credentials, logs, and memory")
	replayPaths := flag.String("replay", "", "comma-separated session JSONL files to replay")
	flag.Parse()
	paths, err := resolveStartupPaths(*configOverride, *applicationOverride, *dataOverride)
	if err != nil {
		log.Fatal(err)
	}
	cfg, migrated, created, err := config.LoadWithTemplate(paths.Config, filepath.Join(paths.Application, "harness.example.json"))
	if err != nil {
		log.Fatal(err)
	}
	workspaceRoot, err := filepath.Abs(cfg.Workspace)
	if err != nil {
		log.Fatal(err)
	}
	paths.Workspace = filepath.Clean(workspaceRoot)
	roots := webserver.RuntimeRoots{Application: paths.Application, Data: paths.Data, Workspace: paths.Workspace}
	if created {
		log.Printf("created %s from %s - set servers[0].base_url and model", paths.Config, filepath.Join(paths.Application, "harness.example.json"))
	}
	if migrated {
		log.Printf("migrated %s: server → servers[local]", filepath.Base(paths.Config))
	}
	for _, notice := range cfg.LoadNotices {
		log.Printf("config migration: %s", notice)
	}
	facts := readServingFacts(filepath.Join(paths.Application, "SERVING.md"))
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
		web := webserver.New(cfg, paths.Config, filepath.Join(paths.Application, "web"), roots, events.NewBus())
		web.SetReplay(replay)
		if err := serve(cfg, web.Handler()); err != nil {
			log.Fatal(err)
		}
		return
	}
	logDir := cfg.LogDir
	if !filepath.IsAbs(logDir) {
		logDir = filepath.Join(paths.Data, logDir)
	}
	writers, err := events.NewWriters(logDir)
	if err != nil {
		log.Fatal(err)
	}
	defer func() {
		if err := writers.Close(); err != nil {
			log.Printf("close event logs: %v", err)
		}
	}()
	bus := events.NewBus()
	bus.SetSink(writers.Write)
	web := webserver.New(cfg, paths.Config, filepath.Join(paths.Application, "web"), roots, bus)
	for _, notice := range cfg.LoadNotices {
		bus.Publish(events.New(events.ConfigChanged, "", "", map[string]any{"config": cfg.Masked(), "notice": notice}))
	}
	memoryManager := memory.New(paths.Data, web.ConfigSnapshot, func(ctx context.Context, serverID, text string) (int, error) {
		profile, ok := web.Profile(serverID)
		if !ok || !profile.Capabilities.Tokenize {
			return 0, fmt.Errorf("tokenizer unavailable")
		}
		return llm.New(profile).Tokenize(ctx, text, false)
	})
	registry := session.NewRegistry(bus, writers, web.Profile, cfg.Run.MaxTurns, web.ConfigSnapshot)
	registry.SetMemoryLoader(memoryManager.Load)
	web.SetRegistry(registry)
	renderer, err := agent.LoadTemplate(filepath.Join(paths.Application, "prompts", "system.md"))
	if err != nil {
		log.Fatal(err)
	}
	workspaces := session.NewWorkspaceRegistry()
	coordinator := tools.NewFileCoordinator(workspaces, registry.Label, bus)
	credentialStore := credential.New(paths.Data)
	fileIdentity := tools.NewFileIdentity(credentialStore)
	fileIdentity.Configure(*cfg)
	shellTool := tools.NewShell(cfg.Shell)
	shellTool.SetFileCoordinator(coordinator)
	shellTool.Configure(*cfg)
	shellTool.SetCredentialStore(credentialStore)
	shellTool.SetIdentityReporter(func(status tools.ShellIdentityStatus) {
		bus.Publish(events.New(events.ShellIdentity, "", "", status))
	})
	web.SetShellSecurity(credentialStore, shellTool)
	web.SetServiceAccountManager(serviceaccount.New(filepath.Join(paths.Application, "scripts", "setup-service-account.ps1")))
	web.SetHardeningManager(hardening.New(
		filepath.Join(paths.Application, "scripts", "apply-acls.ps1"),
		filepath.Join(paths.Application, "scripts", "apply-firewall-rule.ps1"),
		filepath.Join(paths.Application, "scripts", "apply-hardening.ps1"),
	))
	toolRegistry := tools.New(
		fileIdentity.Wrap(tools.NewReadFile(cfg.Tools.ReadFile)),
		fileIdentity.Wrap(tools.NewListDir(cfg.Tools.ListDir)),
		fileIdentity.Wrap(tools.NewWriteFile(coordinator)),
		fileIdentity.Wrap(tools.NewEditFile(coordinator)),
		fileIdentity.Wrap(tools.NewGrep(cfg.Tools.Grep, cfg.Tools.ListDir)),
		shellTool,
		tools.NewRemember(memoryManager, bus),
		tools.NewRecall(memoryManager),
		tools.NewFetch(cfg.Tools.Fetch),
		fileIdentity.Wrap(tools.NewGlob()),
	)
	runner := agent.NewRunner(bus, toolRegistry, renderer, web.Profile, web.ConfigSnapshot)
	scheduler := agent.NewScheduler(runner, registry, bus, web.ConfigSnapshot)
	web.SetRuntime(scheduler, runner, renderer)
	mainServerID := startupServerID(cfg.Servers, registry.ProfileRunnable)
	if ready, reason := registry.ProfileRunnable(mainServerID); ready {
		log.Printf("startup profile %s ready from saved capabilities", mainServerID)
	} else {
		log.Printf("startup profile %s not runnable: %s; use Connections > Test", mainServerID, reason)
	}
	mainSession, err := registry.Create("main", mainServerID, cfg.Workspace)
	if err != nil {
		log.Fatal(err)
	}
	runner.PublishBudget(context.Background(), mainSession)
	if err := serve(cfg, web.Handler()); err != nil {
		log.Fatal(err)
	}
}

type startupPaths struct {
	Application string
	Data        string
	Workspace   string
	Config      string
}

func resolveStartupPaths(configOverride, applicationOverride, dataOverride string) (startupPaths, error) {
	cwd, err := os.Getwd()
	if err != nil {
		return startupPaths{}, fmt.Errorf("resolve working directory: %w", err)
	}
	application := applicationOverride
	if application == "" {
		application = cwd
	}
	application, err = filepath.Abs(application)
	if err != nil {
		return startupPaths{}, fmt.Errorf("resolve application root: %w", err)
	}

	localData := ""
	if base := strings.TrimSpace(os.Getenv("LOCALAPPDATA")); base != "" {
		localData = filepath.Join(base, "Agent_b")
	}
	configPath := strings.TrimSpace(configOverride)
	data := strings.TrimSpace(dataOverride)
	if configPath == "" {
		configPath = strings.TrimSpace(os.Getenv("AGENTB_CONFIG"))
	}
	if configPath != "" && data == "" {
		if localData != "" {
			data = localData
		} else {
			data = cwd
		}
	}
	if configPath == "" && localData != "" {
		candidate := filepath.Join(localData, "harness.json")
		if _, statErr := os.Stat(candidate); statErr == nil {
			configPath = candidate
			if data == "" {
				data = localData
			}
		} else if !os.IsNotExist(statErr) {
			return startupPaths{}, fmt.Errorf("inspect installed configuration: %w", statErr)
		}
	}
	if configPath == "" {
		configPath = filepath.Join(cwd, "harness.json")
		if data == "" {
			data = cwd
		}
	}
	configPath, err = filepath.Abs(configPath)
	if err != nil {
		return startupPaths{}, fmt.Errorf("resolve configuration path: %w", err)
	}
	data, err = filepath.Abs(data)
	if err != nil {
		return startupPaths{}, fmt.Errorf("resolve data root: %w", err)
	}
	return startupPaths{Application: filepath.Clean(application), Data: filepath.Clean(data), Config: filepath.Clean(configPath)}, nil
}

func startupServerID(profiles []config.Profile, runnable func(string) (bool, string)) string {
	if len(profiles) == 0 {
		return ""
	}
	for index := range profiles {
		if ready, _ := runnable(profiles[index].ID); ready {
			return profiles[index].ID
		}
	}
	return profiles[0].ID
}

func startupElevationError(elevated bool) error {
	if !elevated {
		return nil
	}
	return fmt.Errorf("SECURITY: Agent_b refuses to run with an elevated Administrator token; local administrators can launch it normally by double-clicking start-Agent_b.cmd in File Explorer (do not use Run as administrator)")
}

func serve(cfg *config.Config, handler http.Handler) error {
	httpServer := &http.Server{Addr: cfg.Listen, Handler: handler, ReadHeaderTimeout: 10 * time.Second}
	errors := make(chan error, 1)
	go func() {
		log.Printf("Agent_b listening on http://%s", cfg.Listen)
		errors <- httpServer.ListenAndServe()
	}()
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
// malformed values remain zero-valued so a documentation issue cannot stop Agent_b.
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
