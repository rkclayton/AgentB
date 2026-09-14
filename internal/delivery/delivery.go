package delivery

import (
	"crypto/sha256"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"

	"harness/internal/config"
	"harness/internal/events"
	"harness/internal/session"
	"harness/internal/tools"
)

type Source struct {
	Path  string `json:"path"`
	Bytes int64  `json:"bytes"`
}

type Item struct {
	SourcePath    string `json:"source_path"`
	DeliveredPath string `json:"delivered_path,omitempty"`
	ExchangePath  string `json:"exchange_path,omitempty"`
	Bytes         int64  `json:"bytes,omitempty"`
	SHA256        string `json:"sha256,omitempty"`
	Status        string `json:"status"`
	Error         string `json:"error,omitempty"`
}

type Result struct {
	Mode           string `json:"mode"`
	ExchangeFolder string `json:"exchange_folder,omitempty"`
	Items          []Item `json:"items"`
}

type Manager struct {
	bus *events.Bus
	cfg func() config.Config
}

func New(bus *events.Bus, cfg func() config.Config) *Manager {
	return &Manager{bus: bus, cfg: cfg}
}

func (m *Manager) Deliver(s *session.Session, runID string, sources []Source) Result {
	cfg := m.cfg()
	result := Result{Mode: cfg.Deliver.Mode, Items: []Item{}}
	if cfg.Deliver.Mode == config.DeliverModeFolder || cfg.Deliver.Mode == config.DeliverModeBoth {
		folder, err := cfg.ResolvedExchangeFolder()
		if err != nil {
			for _, source := range sources {
				result.Items = append(result.Items, Item{SourcePath: source.Path, Status: "failed", Error: err.Error()})
			}
		} else if err := os.MkdirAll(folder, 0o755); err != nil {
			result.ExchangeFolder = folder
			for _, source := range sources {
				result.Items = append(result.Items, Item{SourcePath: source.Path, Status: "failed", Error: fmt.Sprintf("create exchange folder: %v", err)})
			}
		} else {
			result.ExchangeFolder = folder
			for _, source := range sources {
				result.Items = append(result.Items, copySource(s.Workspace, folder, source))
			}
		}
	}
	if m.bus != nil {
		m.bus.Publish(events.New(events.FilesDelivered, s.ID, runID, result))
	}
	return result
}

func copySource(workspace, folder string, source Source) Item {
	item := Item{SourcePath: source.Path, Bytes: source.Bytes, Status: "failed"}
	resolved, err := tools.Resolve(workspace, source.Path)
	if err != nil {
		item.Error = "source is outside the folder"
		return item
	}
	input, err := os.Open(resolved)
	if err != nil {
		item.Error = fmt.Sprintf("open source: %v", err)
		return item
	}
	defer input.Close()
	info, err := input.Stat()
	if err != nil || !info.Mode().IsRegular() {
		item.Error = "source is not a regular file"
		return item
	}
	hash := sha256.New()
	if _, err := io.Copy(hash, input); err != nil {
		item.Error = fmt.Sprintf("hash source: %v", err)
		return item
	}
	item.Bytes = info.Size()
	item.SHA256 = fmt.Sprintf("%x", hash.Sum(nil))
	if _, err := input.Seek(0, io.SeekStart); err != nil {
		item.Error = fmt.Sprintf("rewind source: %v", err)
		return item
	}
	base := filepath.Base(resolved)
	for suffix := 1; ; suffix++ {
		name := collisionName(base, suffix)
		target := filepath.Join(folder, name)
		if existing, err := fileSHA256(target); err == nil {
			if existing == item.SHA256 {
				item.DeliveredPath, item.ExchangePath, item.Status = target, name, "identical"
				return item
			}
			continue
		} else if !os.IsNotExist(err) {
			item.Error = fmt.Sprintf("inspect destination: %v", err)
			return item
		}
		output, err := os.OpenFile(target, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
		if os.IsExist(err) {
			continue
		}
		if err != nil {
			item.Error = fmt.Sprintf("create destination: %v", err)
			return item
		}
		_, copyErr := io.Copy(output, input)
		if copyErr == nil {
			copyErr = output.Sync()
		}
		if closeErr := output.Close(); copyErr == nil {
			copyErr = closeErr
		}
		if copyErr != nil {
			_ = os.Remove(target)
			item.Error = fmt.Sprintf("copy destination: %v", copyErr)
			return item
		}
		item.DeliveredPath, item.ExchangePath, item.Status = target, name, "copied"
		return item
	}
}

func collisionName(base string, suffix int) string {
	if suffix == 1 {
		return base
	}
	extension := filepath.Ext(base)
	stem := base[:len(base)-len(extension)]
	return fmt.Sprintf("%s (%d)%s", stem, suffix, extension)
}

func fileSHA256(path string) (string, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer file.Close()
	hash := sha256.New()
	if _, err := io.Copy(hash, file); err != nil {
		return "", err
	}
	return fmt.Sprintf("%x", hash.Sum(nil)), nil
}

func SortedSources(values map[string]Source) []Source {
	result := make([]Source, 0, len(values))
	for _, value := range values {
		result = append(result, value)
	}
	sort.Slice(result, func(i, j int) bool { return result[i].Path < result[j].Path })
	return result
}
