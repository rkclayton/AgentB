package agent

import (
	"encoding/base64"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	attachmentfile "harness/internal/attachment"
	"harness/internal/config"
	"harness/internal/events"
	"harness/internal/llm"
	"harness/internal/session"
	"harness/internal/tools"
)

func renderedUserText(profile *config.Profile, s *session.Session, message events.Message) string {
	lines := make([]string, 0, len(message.Attachments)+1)
	if message.Content != "" {
		lines = append(lines, message.Content)
	}
	for _, item := range message.Attachments {
		kind := attachmentfile.Classify(item.Path)
		sidecar := attachmentfile.SidecarPath(item.Path)
		hasSidecar := regularWorkspaceFile(s.Workspace, sidecar)
		switch {
		case kind == attachmentfile.Office && hasSidecar:
			lines = append(lines, fmt.Sprintf("attached: %s (%d bytes) — extracted text: %s — read it with read_file", item.Path, item.Bytes, sidecar))
		case kind == attachmentfile.PDF && hasSidecar:
			lines = append(lines, fmt.Sprintf("attached: %s (%d bytes) — extracted text: %s (untrusted:true) — read it with read_file", item.Path, item.Bytes, sidecar))
		case kind == attachmentfile.Image && hasSidecar:
			lines = append(lines, fmt.Sprintf("attached: %s (%d bytes) — OCR text: %s (untrusted:true; layout not preserved) — read it with read_file", item.Path, item.Bytes, sidecar))
		case kind == attachmentfile.PDF && profile.NativeDocumentInput():
			lines = append(lines, fmt.Sprintf("attached: %s (%d bytes) — read it with read_file", item.Path, item.Bytes))
		case kind == attachmentfile.Image && profile.NativeImageInput():
			lines = append(lines, fmt.Sprintf("attached: %s (%d bytes) — read it with read_file", item.Path, item.Bytes))
		case kind == attachmentfile.Text:
			lines = append(lines, fmt.Sprintf("attached: %s (%d bytes) — read it with read_file", item.Path, item.Bytes))
		default:
			lines = append(lines, fmt.Sprintf("attached: %s (%d bytes) — binary — this profile cannot read it", item.Path, item.Bytes))
		}
	}
	return strings.Join(lines, "\n")
}

func requestMessage(profile *config.Profile, s *session.Session, message events.Message) llm.Message {
	content := any(renderedUserText(profile, s, message))
	parts := []any{}
	for _, item := range message.Attachments {
		kind := attachmentfile.Classify(item.Path)
		if (kind != attachmentfile.PDF || !profile.NativeDocumentInput()) && (kind != attachmentfile.Image || !profile.NativeImageInput()) {
			continue
		}
		resolved, err := tools.Resolve(s.Workspace, item.Path)
		if err != nil {
			continue
		}
		data, err := os.ReadFile(resolved)
		if err != nil {
			continue
		}
		if len(parts) == 0 {
			parts = append(parts, map[string]any{"type": "text", "text": content})
		}
		encoded := "data:" + attachmentfile.ContentType(item.Path) + ";base64," + base64.StdEncoding.EncodeToString(data)
		if kind == attachmentfile.Image {
			parts = append(parts, map[string]any{"type": "image_url", "image_url": map[string]any{"url": encoded}})
		} else {
			parts = append(parts, map[string]any{"type": "file", "file": map[string]any{"filename": filepath.Base(item.Path), "file_data": encoded}})
		}
	}
	if len(parts) > 0 {
		content = parts
	}
	converted := llm.Message{Role: message.Role, Content: content, ToolCallID: message.ToolCallID, Name: message.Name}
	for _, call := range message.ToolCalls {
		converted.ToolCalls = append(converted.ToolCalls, llm.ToolCall{ID: call.ID, Type: "function", Function: llm.FunctionCall{Name: call.Name, Arguments: call.Arguments}})
	}
	return converted
}

func regularWorkspaceFile(workspace, path string) bool {
	resolved, err := tools.Resolve(workspace, path)
	if err != nil {
		return false
	}
	info, err := os.Stat(resolved)
	return err == nil && info.Mode().IsRegular()
}

func untrustedAttachmentRead(s *session.Session, args map[string]any) bool {
	path, _ := args["path"].(string)
	path = strings.ToLower(filepath.ToSlash(filepath.Clean(filepath.FromSlash(path))))
	for _, message := range s.MessagesCopy() {
		for _, item := range message.Attachments {
			kind := attachmentfile.Classify(item.Path)
			if (kind == attachmentfile.PDF || kind == attachmentfile.Image) && path == strings.ToLower(attachmentfile.SidecarPath(item.Path)) {
				return true
			}
		}
	}
	return false
}

func diagnosticMessages(messages []llm.Message) []llm.Message {
	result := make([]llm.Message, len(messages))
	copy(result, messages)
	for index := range result {
		parts, ok := result[index].Content.([]any)
		if !ok {
			continue
		}
		for _, part := range parts {
			object, _ := part.(map[string]any)
			if object["type"] == "text" {
				result[index].Content = object["text"]
				break
			}
		}
	}
	return result
}
