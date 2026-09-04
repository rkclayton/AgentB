//go:build windows

package credential

import (
	"bytes"
	"crypto/rand"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestStoreRoundTripAndClear(t *testing.T) {
	dataRoot := t.TempDir()
	store := New(dataRoot)
	if store.Path() != filepath.Join(dataRoot, FileName) {
		t.Fatalf("credential path = %q, want data-root path", store.Path())
	}
	if _, err := store.Read(); !errors.Is(err, ErrNotStored) {
		t.Fatalf("absent Read error = %v, want ErrNotStored", err)
	}
	if status := store.Status(); status.Stored || status.StoredAt != "" {
		t.Fatalf("absent status = %+v", status)
	}

	password := make([]byte, 32)
	if _, err := rand.Read(password); err != nil {
		t.Fatal(err)
	}
	if err := store.Write(password); err != nil {
		t.Fatal(err)
	}
	if status := store.Status(); !status.Stored || status.StoredAt == "" {
		t.Fatalf("stored status = %+v", status)
	}
	got, err := store.Read()
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, password) {
		t.Fatal("credential did not round-trip")
	}
	if bytes.Equal(got, mustReadFile(t, store.Path())) {
		t.Fatal("credential store contains plaintext")
	}
	second := make([]byte, 32)
	if _, err := rand.Read(second); err != nil {
		t.Fatal(err)
	}
	if err := store.Write(second); err != nil {
		t.Fatalf("replace: %v", err)
	}
	got, err = store.Read()
	if err != nil || !bytes.Equal(got, second) {
		t.Fatalf("replacement round trip: got %x, err %v", got, err)
	}
	if err := store.Clear(); err != nil {
		t.Fatal(err)
	}
	if err := store.Clear(); err != nil {
		t.Fatalf("second Clear: %v", err)
	}
	if _, err := store.Read(); !errors.Is(err, ErrNotStored) {
		t.Fatalf("Read after Clear error = %v, want ErrNotStored", err)
	}
}

func mustReadFile(t *testing.T, path string) []byte {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return data
}
