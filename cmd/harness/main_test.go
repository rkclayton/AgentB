package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"harness/internal/config"
	"harness/internal/events"
	"harness/internal/session"
)

func TestRetainedChatsRestoreWithoutOperationalLogsAndDeleteExplicitly(t *testing.T) {
	root, workspace := t.TempDir(), t.TempDir()
	logs := filepath.Join(root, "logs")
	cfg := config.Defaults(workspace)
	profile := &cfg.Servers[0]
	profiles := func(id string) (*config.Profile, bool) { return profile, id == profile.ID }

	firstWriters, err := events.NewWriters(logs)
	if err != nil {
		t.Fatal(err)
	}
	firstBus := events.NewBus()
	firstBus.SetDurableSink(firstWriters.WriteRecord, nil, nil)
	firstRegistry := session.NewRegistry(firstBus, firstWriters, profiles, 40, func() config.Config { return cfg })
	item, err := firstRegistry.Create("main", cfg.DefaultAgentID(), workspace)
	if err != nil {
		t.Fatal(err)
	}
	message := events.Message{ID: "m1", Role: "user", Category: "history", Content: "retained words"}
	item.Append(message)
	firstBus.Publish(events.New(events.MessageAppended, item.ID, "", map[string]any{"message": message}))
	if err := firstRegistry.RenameBy(item.ID, "durable name", "c"); err != nil {
		t.Fatal(err)
	}
	other, err := firstRegistry.CreateLike(item.ID)
	if err != nil {
		t.Fatal(err)
	}
	otherMessage := events.Message{ID: "m2", Role: "user", Category: "history", Content: "independent context"}
	other.Append(otherMessage)
	firstBus.Publish(events.New(events.MessageAppended, other.ID, "", map[string]any{"message": otherMessage}))
	if err := firstWriters.Close(); err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(logs); err != nil {
		t.Fatal(err)
	}

	secondWriters, err := events.NewWriters(logs)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = secondWriters.Close() })
	secondBus := events.NewBus()
	secondBus.SetDurableSink(secondWriters.WriteRecord, nil, nil)
	secondRegistry := session.NewRegistry(secondBus, secondWriters, profiles, 40, func() config.Config { return cfg })
	restored, err := restoreRetainedChats(secondWriters, secondRegistry)
	if err != nil {
		t.Fatal(err)
	}
	if len(restored) != 2 {
		t.Fatalf("restored chats=%d", len(restored))
	}
	byID := map[string]session.Snapshot{}
	for _, restoredChat := range restored {
		byID[restoredChat.ID] = restoredChat.Snapshot()
	}
	if snapshot := byID[item.ID]; snapshot.Label != "durable name" || len(snapshot.Messages) != 1 || snapshot.Messages[0].Content != "retained words" {
		t.Fatalf("restored first snapshot=%+v", snapshot)
	}
	if snapshot := byID[other.ID]; len(snapshot.Messages) != 1 || snapshot.Messages[0].Content != "independent context" {
		t.Fatalf("restored second snapshot=%+v", snapshot)
	}
	for _, restoredChat := range restored {
		if err := secondRegistry.Close(restoredChat.ID); err != nil {
			t.Fatal(err)
		}
		if _, err := secondRegistry.Delete(restoredChat.ID); err != nil {
			t.Fatal(err)
		}
	}
	paths, err := secondWriters.DurableChatPaths()
	if err != nil || len(paths) != 0 {
		t.Fatalf("durable paths after explicit delete=%v err=%v", paths, err)
	}
}

func TestRestoreRetainedChatsBootstrapsNewestLegacyOperationalGeneration(t *testing.T) {
	root, workspace := t.TempDir(), t.TempDir()
	logs := filepath.Join(root, "logs")
	cfg := config.Defaults(workspace)
	profile := &cfg.Servers[0]
	profiles := func(id string) (*config.Profile, bool) { return profile, id == profile.ID }
	firstWriters, err := events.NewWriters(logs)
	if err != nil {
		t.Fatal(err)
	}
	firstBus := events.NewBus()
	firstBus.SetDurableSink(firstWriters.WriteRecord, nil, nil)
	firstRegistry := session.NewRegistry(firstBus, firstWriters, profiles, 40, func() config.Config { return cfg })
	item, err := firstRegistry.Create("upgrade chat", cfg.DefaultAgentID(), workspace)
	if err != nil {
		t.Fatal(err)
	}
	message := events.Message{ID: "legacy", Role: "user", Category: "history", Content: "before upgrade"}
	item.Append(message)
	firstBus.Publish(events.New(events.MessageAppended, item.ID, "", map[string]any{"message": message}))
	if err := firstWriters.Close(); err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(filepath.Join(root, "chats")); err != nil {
		t.Fatal(err)
	}

	secondWriters, err := events.NewWriters(logs)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = secondWriters.Close() })
	secondBus := events.NewBus()
	secondBus.SetDurableSink(secondWriters.WriteRecord, nil, nil)
	secondRegistry := session.NewRegistry(secondBus, secondWriters, profiles, 40, func() config.Config { return cfg })
	restored, err := restoreRetainedChats(secondWriters, secondRegistry)
	if err != nil {
		t.Fatal(err)
	}
	if len(restored) != 1 || restored[0].Snapshot().Messages[0].Content != "before upgrade" {
		t.Fatalf("legacy restore=%+v", restored)
	}
	paths, err := secondWriters.DurableChatPaths()
	if err != nil || len(paths) != 1 {
		t.Fatalf("seeded durable paths=%v err=%v", paths, err)
	}
}

func TestReadServingFactsCompleteness(t *testing.T) {
	path := filepath.Join(t.TempDir(), "SERVING.md")
	if facts := readServingFacts(path); facts.Complete {
		t.Fatal("missing file reported complete")
	}
	if err := os.WriteFile(path, []byte("tokenize_idle_ms=8\ntokenize_blocks_on_slot=no\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if facts := readServingFacts(path); facts.Complete {
		t.Fatal("partial file reported complete")
	}
	if err := os.WriteFile(path, []byte("tokenize_idle_ms=8\ntokenize_busy_ms=9\ntokenize_blocks_on_slot=no\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	facts := readServingFacts(path)
	if !facts.Complete || facts.TokenizeIdleMS != 8 || facts.TokenizeBusyMS != 9 || facts.TokenizeBlocksOnSlot != "no" {
		t.Fatalf("unexpected facts: %+v", facts)
	}
}

func TestStartupElevationGuard(t *testing.T) {
	if err := startupElevationError(false); err != nil {
		t.Fatalf("standard token refused: %v", err)
	}
	err := startupElevationError(true)
	if err == nil || !strings.Contains(err.Error(), "refuses to run") || !strings.Contains(err.Error(), "File Explorer") {
		t.Fatalf("elevated token error = %v", err)
	}
}

func TestResolveStartupPathsPrecedence(t *testing.T) {
	cwd := t.TempDir()
	t.Chdir(cwd)
	localBase := t.TempDir()
	t.Setenv("LOCALAPPDATA", localBase)
	installedConfig := filepath.Join(localBase, "Agent_b", "harness.json")
	if err := os.MkdirAll(filepath.Dir(installedConfig), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(installedConfig, []byte("{}"), 0o600); err != nil {
		t.Fatal(err)
	}
	environmentConfig := filepath.Join(t.TempDir(), "environment.json")
	explicitConfig := filepath.Join(t.TempDir(), "explicit.json")
	t.Setenv("AGENTB_CONFIG", environmentConfig)

	paths, err := resolveStartupPaths(explicitConfig, filepath.Join(cwd, "application"), filepath.Join(cwd, "data"))
	if err != nil {
		t.Fatal(err)
	}
	if paths.Config != explicitConfig || paths.Data != filepath.Join(cwd, "data") || paths.Application != filepath.Join(cwd, "application") {
		t.Fatalf("explicit paths = %+v", paths)
	}

	paths, err = resolveStartupPaths("", "", "")
	if err != nil {
		t.Fatal(err)
	}
	if paths.Config != environmentConfig || paths.Data != filepath.Join(localBase, "Agent_b") {
		t.Fatalf("environment paths = %+v", paths)
	}

	t.Setenv("AGENTB_CONFIG", "")
	paths, err = resolveStartupPaths("", "", "")
	if err != nil {
		t.Fatal(err)
	}
	if paths.Config != installedConfig || paths.Data != filepath.Join(localBase, "Agent_b") {
		t.Fatalf("installed paths = %+v", paths)
	}

	if err := os.Remove(installedConfig); err != nil {
		t.Fatal(err)
	}
	paths, err = resolveStartupPaths("", "", "")
	if err != nil {
		t.Fatal(err)
	}
	if paths.Config != filepath.Join(cwd, "harness.json") || paths.Data != cwd || paths.Application != cwd {
		t.Fatalf("development paths = %+v", paths)
	}
}
