//go:build windows

package web

import (
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestRequireOperatorHTTPClientRejectsAgentBProcess(t *testing.T) {
	result := make(chan error, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		result <- requireOperatorHTTPClient(request)
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()

	response, err := server.Client().Get(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	select {
	case err := <-result:
		if err == nil || !strings.Contains(err.Error(), "descendants") {
			t.Fatalf("error=%v, want Agent_b descendant refusal", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal(fmt.Errorf("timed out waiting for client identity check"))
	}
}

func TestRequireOperatorHTTPClientAcceptsExternalOperatorProcess(t *testing.T) {
	addressPath := filepath.Join(t.TempDir(), "address")
	command := exec.Command(os.Args[0], "-test.run=^TestOperatorRequestHelper$")
	command.Env = append(os.Environ(), "AGENTB_OPERATOR_REQUEST_HELPER=1", "AGENTB_OPERATOR_REQUEST_ADDRESS="+addressPath)
	if err := command.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() {
		if command.ProcessState == nil {
			_ = command.Process.Kill()
			_, _ = command.Process.Wait()
		}
	}()
	deadline := time.Now().Add(3 * time.Second)
	var address []byte
	for time.Now().Before(deadline) {
		address, _ = os.ReadFile(addressPath)
		if len(address) > 0 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if len(address) == 0 {
		t.Fatal("helper did not publish its address")
	}
	response, err := http.Get("http://" + string(address))
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(response.Body)
	response.Body.Close()
	if response.StatusCode != http.StatusNoContent {
		t.Fatalf("status=%d body=%s", response.StatusCode, body)
	}
	if err := command.Wait(); err != nil {
		t.Fatal(err)
	}
}

func TestOperatorRequestHelper(t *testing.T) {
	if os.Getenv("AGENTB_OPERATOR_REQUEST_HELPER") != "1" {
		return
	}
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addressPath := os.Getenv("AGENTB_OPERATOR_REQUEST_ADDRESS")
	if err := os.WriteFile(addressPath, []byte(listener.Addr().String()), 0o600); err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	server := &http.Server{Handler: http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if err := requireOperatorHTTPClient(request); err != nil {
			http.Error(writer, err.Error(), http.StatusForbidden)
		} else {
			writer.WriteHeader(http.StatusNoContent)
		}
		close(done)
	})}
	go func() { _ = server.Serve(listener) }()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal(fmt.Errorf("timed out waiting for parent request"))
	}
	_ = server.Close()
}
