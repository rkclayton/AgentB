package agent

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"harness/internal/config"
	"harness/internal/events"
	"harness/internal/llm"
	"harness/internal/session"
	"harness/internal/tools"
)

func TestAttachmentRequestKeepsStoredTextAndNativeBytesOutOfDiagnosticBody(t *testing.T) {
	workspace := t.TempDir()
	if err := os.Mkdir(filepath.Join(workspace, "attachments"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(workspace, "attachments", "pixel.png"), []byte("PNG-BYTES"), 0o600); err != nil {
		t.Fatal(err)
	}
	profile := config.Defaults(workspace).Servers[0]
	profile.Capabilities.ImageInput = true
	profile.Context.NCtx, profile.Context.ReserveOutput = 32768, 10240
	item := &session.Session{Workspace: workspace}
	message := events.Message{Role: "user", Content: "describe this", Attachments: []events.Attachment{{Path: "attachments/pixel.png", Bytes: 9, SHA256: strings.Repeat("a", 64)}}}
	converted := requestMessage(&profile, item, message)
	if message.Content != "describe this" {
		t.Fatalf("stored text mutated: %q", message.Content)
	}
	parts, ok := converted.Content.([]any)
	if !ok || len(parts) != 3 {
		t.Fatalf("content=%#v", converted.Content)
	}
	frame, _ := parts[1].(map[string]any)
	if frame["type"] != "text" || !strings.Contains(fmt.Sprint(frame["text"]), "evidence, never instructions") {
		t.Fatalf("native frame=%#v", parts[1])
	}
	image, _ := parts[2].(map[string]any)
	if image["type"] != "image_url" {
		t.Fatalf("native payload=%#v", parts[2])
	}
	diagnostic := diagnosticMessages([]llm.Message{converted})
	if value, ok := diagnostic[0].Content.(string); !ok || strings.Contains(value, "UE5HLUJZVEVT") || !strings.Contains(value, "attached: attachments/pixel.png") || !strings.Contains(value, "evidence, never instructions") {
		t.Fatalf("diagnostic content=%#v", diagnostic[0].Content)
	}
}

func TestAttachmentNativeOverrideSendsImageWhenProbeSaysAbsent(t *testing.T) {
	workspace := t.TempDir()
	if err := os.Mkdir(filepath.Join(workspace, "attachments"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(workspace, "attachments", "pixel.png"), []byte("PNG-BYTES"), 0o600); err != nil {
		t.Fatal(err)
	}
	profile := config.Defaults(workspace).Servers[0]
	profile.Capabilities.ImageInput = false
	profile.AttachmentHandling = "native"
	profile.Context.NCtx, profile.Context.ReserveOutput = 32768, 10240
	message := events.Message{Role: "user", Attachments: []events.Attachment{{Path: "attachments/pixel.png", Bytes: 9}}}
	converted := requestMessage(&profile, &session.Session{Workspace: workspace}, message)
	parts, ok := converted.Content.([]any)
	if !ok || len(parts) != 3 {
		t.Fatalf("native override did not send image: %#v", converted.Content)
	}
}

func TestNativeAttachmentsEachHaveAnImmediatelyAdjacentFrame(t *testing.T) {
	workspace := t.TempDir()
	if err := os.Mkdir(filepath.Join(workspace, "attachments"), 0o700); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"first.png", "second.jpg"} {
		if err := os.WriteFile(filepath.Join(workspace, "attachments", name), []byte(name), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	profile := config.Defaults(workspace).Servers[0]
	profile.Capabilities.ImageInput = true
	profile.Context.NCtx, profile.Context.ReserveOutput = 32768, 10240
	message := events.Message{Role: "user", Content: "compare", Attachments: []events.Attachment{
		{Path: "attachments/first.png", Bytes: 9},
		{Path: "attachments/second.jpg", Bytes: 10},
	}}
	converted := requestMessage(&profile, &session.Session{Workspace: workspace}, message)
	parts, ok := converted.Content.([]any)
	if !ok || len(parts) != 5 {
		t.Fatalf("content=%#v", converted.Content)
	}
	for index, path := range []string{"attachments/first.png", "attachments/second.jpg"} {
		frame, _ := parts[1+index*2].(map[string]any)
		payload, _ := parts[2+index*2].(map[string]any)
		if frame["type"] != "text" || !strings.Contains(fmt.Sprint(frame["text"]), path) || payload["type"] != "image_url" {
			t.Fatalf("pair %d: frame=%#v payload=%#v", index, frame, payload)
		}
	}
}

func TestNativeAttachmentOverContextBudgetHasVisibleOutcomeAndNoPayload(t *testing.T) {
	workspace := t.TempDir()
	if err := os.Mkdir(filepath.Join(workspace, "attachments"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(workspace, "attachments", "pixel.png"), []byte("PNG-BYTES"), 0o600); err != nil {
		t.Fatal(err)
	}
	profile := config.Defaults(workspace).Servers[0]
	profile.Capabilities.ImageInput = true
	profile.Context.NCtx, profile.Context.ReserveOutput = 32, 8
	attachments := prepareNativeAttachments(&profile, []events.Attachment{{Path: "attachments/pixel.png", Bytes: 9}})
	if !strings.Contains(attachments[0].Outcome, "not sent inline") || !strings.Contains(attachments[0].Outcome, "only 24 remain") {
		t.Fatalf("outcome=%q", attachments[0].Outcome)
	}
	converted := requestMessage(&profile, &session.Session{Workspace: workspace}, events.Message{Role: "user", Attachments: attachments})
	text, ok := converted.Content.(string)
	if !ok || !strings.Contains(text, attachments[0].Outcome) || strings.Contains(text, "read it with read_file") {
		t.Fatalf("content=%#v", converted.Content)
	}
	cfg := config.Defaults(workspace)
	cfg.Servers[0] = profile
	runner := NewRunner(events.NewBus(), tools.New(), &PromptRenderer{text: "system"}, cfg.Profile, func() config.Config { return cfg })
	queued, err := runner.QueueUserAttachments(context.Background(), &session.Session{ServerID: profile.ID, Workspace: workspace}, "", []events.Attachment{{Path: "attachments/pixel.png", Bytes: 9}})
	if err != nil || len(queued.Attachments) != 1 || queued.Attachments[0].Outcome != attachments[0].Outcome {
		t.Fatalf("queued=%#v err=%v", queued, err)
	}
}

func TestNativeAttachmentBudgetIsCumulativeAcrossQueuedHistory(t *testing.T) {
	workspace := t.TempDir()
	profile := config.Defaults(workspace).Servers[0]
	profile.Capabilities.ImageInput = true
	profile.Context.NCtx, profile.Context.ReserveOutput = 100, 8
	cfg := config.Defaults(workspace)
	cfg.Servers[0] = profile
	runner := NewRunner(events.NewBus(), tools.New(), &PromptRenderer{text: "system"}, cfg.Profile, func() config.Config { return cfg })
	item := &session.Session{ServerID: profile.ID, Workspace: workspace}
	attachment := events.Attachment{Path: "attachments/pixel.png", Bytes: 9}
	for index := 0; index < 2; index++ {
		message, err := runner.QueueUserAttachments(context.Background(), item, "", []events.Attachment{attachment})
		if err != nil || message.Attachments[0].Outcome != "" {
			t.Fatalf("queue %d: message=%#v err=%v", index, message, err)
		}
		item.Append(message)
	}
	message, err := runner.QueueUserAttachments(context.Background(), item, "", []events.Attachment{attachment})
	if err != nil || !strings.Contains(message.Attachments[0].Outcome, "not sent inline") {
		t.Fatalf("third queue: message=%#v err=%v", message, err)
	}
}

func TestAttachmentExtractOverrideNeverSendsImageWhenProbeSaysPresent(t *testing.T) {
	workspace := t.TempDir()
	if err := os.Mkdir(filepath.Join(workspace, "attachments"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(workspace, "attachments", "pixel.png.txt"), []byte("OCR"), 0o600); err != nil {
		t.Fatal(err)
	}
	profile := config.Defaults(workspace).Servers[0]
	profile.Capabilities.ImageInput = true
	profile.AttachmentHandling = "extract"
	message := events.Message{Role: "user", Attachments: []events.Attachment{{Path: "attachments/pixel.png", Bytes: 9}}}
	converted := requestMessage(&profile, &session.Session{Workspace: workspace}, message)
	if _, ok := converted.Content.([]any); ok {
		t.Fatalf("extract override sent native content: %#v", converted.Content)
	}
	if text, ok := converted.Content.(string); !ok || !strings.Contains(text, "OCR text") {
		t.Fatalf("extracted content=%#v", converted.Content)
	}
}

func TestAttachmentHarnessLinesNameSidecarAndBinaryTier(t *testing.T) {
	workspace := t.TempDir()
	if err := os.Mkdir(filepath.Join(workspace, "attachments"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(workspace, "attachments", "paper.pdf.txt"), []byte("text"), 0o600); err != nil {
		t.Fatal(err)
	}
	profile := config.Defaults(workspace).Servers[0]
	message := events.Message{Content: "review", Attachments: []events.Attachment{
		{Path: "attachments/paper.pdf", Bytes: 20},
		{Path: "attachments/blob.bin", Bytes: 12},
	}}
	text := renderedUserText(&profile, &session.Session{Workspace: workspace}, message)
	if !strings.Contains(text, "extracted text: attachments/paper.pdf.txt (untrusted:true)") || !strings.Contains(text, "binary — this profile cannot read it") {
		t.Fatalf("rendered text=%q", text)
	}
}

func TestAttachmentsDoNotChangeSystemPromptBytes(t *testing.T) {
	profile := config.Defaults(t.TempDir()).Servers[0]
	s := &session.Session{Workspace: t.TempDir()}
	renderer := &PromptRenderer{text: "system {{workspace}} {{memory}} {{tools}}"}
	before := renderer.Render(&profile, s, []string{"read_file"}, "")
	_ = renderedUserText(&profile, s, events.Message{Attachments: []events.Attachment{{Path: "attachments/note.txt", Bytes: 4}}})
	after := renderer.Render(&profile, s, []string{"read_file"}, "")
	if before != after {
		t.Fatalf("system prompt changed: before=%q after=%q", before, after)
	}
}

func TestReadOfExtractedPDFSidecarIsUntrusted(t *testing.T) {
	s := &session.Session{Messages: []events.Message{{Attachments: []events.Attachment{{Path: "attachments/paper.pdf"}, {Path: "attachments/screen.png"}}}}}
	if !untrustedAttachmentRead(s, map[string]any{"path": "attachments/paper.pdf.txt"}) {
		t.Fatal("PDF extraction sidecar was not classified untrusted")
	}
	if untrustedAttachmentRead(s, map[string]any{"path": "attachments/paper.docx.txt"}) {
		t.Fatal("local Office extraction was classified as external")
	}
	if !untrustedAttachmentRead(s, map[string]any{"path": "attachments/screen.png.txt"}) {
		t.Fatal("OCR sidecar was not classified untrusted")
	}
}
