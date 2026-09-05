package canary

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

func TestScriptArtifactAndBypassCommandsAreRejected(t *testing.T) {
	executor, err := NewToolExecutor(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		tool string
		args map[string]any
		want string
	}{
		{"write_file", map[string]any{"path": "temp.ps1", "content": "Write-Output unsafe"}, "script artifacts"},
		{"edit_file", map[string]any{"path": "temp.cmd", "old_string": "a", "new_string": "b"}, "script artifacts"},
		{"shell", map[string]any{"command": `powershell -ExecutionPolicy Bypass -Command unsafe`}, "execution-policy bypass"},
		{"shell", map[string]any{"command": `echo unsafe > temp.sh`}, "script artifacts"},
	} {
		raw, err := json.Marshal(test.args)
		if err != nil {
			t.Fatal(err)
		}
		result, ok := executor.Execute(context.Background(), test.tool, raw)
		if ok || !strings.Contains(strings.ToLower(result), test.want) {
			t.Fatalf("tool=%s result=%q ok=%v", test.tool, result, ok)
		}
	}
}
