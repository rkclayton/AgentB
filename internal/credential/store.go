package credential

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

const FileName = ".agentb-shell-credential.dpapi"

var (
	ErrNotStored   = errors.New("shell service-account credential is not stored")
	ErrUnsupported = errors.New("shell service-account credential storage is supported only on Windows")
)

type Status struct {
	Stored   bool   `json:"stored"`
	StoredAt string `json:"stored_at"`
}

type Store struct{ path string }

func New(dataRoot string) *Store {
	if dataRoot == "" {
		dataRoot = "."
	}
	return &Store{path: filepath.Join(dataRoot, FileName)}
}

func (s *Store) Path() string { return s.path }

func (s *Store) Status() Status {
	info, err := os.Stat(s.path)
	if err != nil {
		return Status{}
	}
	return Status{Stored: true, StoredAt: info.ModTime().UTC().Format(time.RFC3339)}
}

func (s *Store) Read() ([]byte, error) {
	protected, err := os.ReadFile(s.path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, ErrNotStored
	}
	if err != nil {
		return nil, fmt.Errorf("read shell credential store: %w", err)
	}
	plain, err := unprotect(protected)
	if err != nil {
		return nil, fmt.Errorf("decrypt shell credential store: %w", err)
	}
	return plain, nil
}

func (s *Store) Write(password []byte) error {
	if len(password) == 0 {
		return errors.New("password is required")
	}
	protected, err := protect(password)
	if err != nil {
		return fmt.Errorf("encrypt shell credential store: %w", err)
	}
	dir := filepath.Dir(s.path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("create shell credential directory: %w", err)
	}
	temp, err := os.CreateTemp(dir, ".agentb-shell-credential-*")
	if err != nil {
		return fmt.Errorf("create shell credential temporary file: %w", err)
	}
	tempPath := temp.Name()
	defer os.Remove(tempPath)
	if err := temp.Chmod(0o600); err != nil {
		temp.Close()
		return fmt.Errorf("protect shell credential temporary file: %w", err)
	}
	if _, err := temp.Write(protected); err != nil {
		temp.Close()
		return fmt.Errorf("write shell credential temporary file: %w", err)
	}
	if err := temp.Sync(); err != nil {
		temp.Close()
		return fmt.Errorf("sync shell credential temporary file: %w", err)
	}
	if err := temp.Close(); err != nil {
		return fmt.Errorf("close shell credential temporary file: %w", err)
	}
	if err := replaceFile(tempPath, s.path); err != nil {
		return fmt.Errorf("replace shell credential store: %w", err)
	}
	return nil
}

func (s *Store) Clear() error {
	err := os.Remove(s.path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("remove shell credential store: %w", err)
	}
	return nil
}
