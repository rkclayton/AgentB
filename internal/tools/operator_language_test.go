package tools

import (
	"encoding/json"
	"strings"
	"testing"

	"harness/internal/config"
	"harness/internal/events"
)

func TestToolDescriptionsAndSchemasUseFolderVocabulary(t *testing.T) {
	cfg := config.Defaults(t.TempDir())
	shell := NewShell(cfg.Shell)
	items := []Tool{
		NewReadFile(cfg.Tools.ReadFile), NewListDir(cfg.Tools.ListDir), NewWriteFile(nil), NewEditFile(nil),
		NewGrep(cfg.Tools.Grep, cfg.Tools.ListDir), NewShell(cfg.Shell), NewRemember(nil, events.NewBus()),
		NewRecall(nil), NewFetch(cfg.Tools.Fetch), NewGlob(cfg.Tools.FindFiles), NewRunScript(shell), NewCallService(nil),
	}
	for _, item := range items {
		encoded, err := json.Marshal(item.Schema())
		if err != nil {
			t.Fatal(err)
		}
		visible := item.Description() + " " + string(encoded)
		if strings.Contains(strings.ToLower(visible), "workspace") {
			t.Fatalf("%s operator vocabulary contains workspace: %s", item.Name(), visible)
		}
	}
}
