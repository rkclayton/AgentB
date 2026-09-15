package session

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	workspaceinfo "harness/internal/workspace"
)

const planManifestName = "plan.json"

type Plan struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	Repo string `json:"repo,omitempty"`
}

type planManifest struct {
	Repo string `json:"repo,omitempty"`
}

func ListPlans(root string) ([]Plan, error) {
	entries, err := os.ReadDir(root)
	if os.IsNotExist(err) {
		return []Plan{}, nil
	}
	if err != nil {
		return nil, err
	}
	values := make([]Plan, 0, len(entries))
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		dir := filepath.Join(root, entry.Name())
		values = append(values, Plan{ID: entry.Name(), Name: planDisplayName(dir), Repo: planRepo(dir)})
	}
	return values, nil
}

func planRepo(dir string) string {
	data, err := os.ReadFile(filepath.Join(dir, planManifestName))
	if err != nil {
		return ""
	}
	var manifest planManifest
	if json.Unmarshal(data, &manifest) != nil {
		return ""
	}
	return manifest.Repo
}

func writePlanRepo(dir, repo string) error {
	data, err := json.MarshalIndent(planManifest{Repo: repo}, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, planManifestName), append(data, '\n'), 0o600)
}

func canonicalRepo(value string) (string, error) {
	abs, key, err := workspaceinfo.Canonical(value)
	if err != nil {
		return "", err
	}
	if strings.TrimSpace(key) == "" {
		return "", fmt.Errorf("repo is required")
	}
	return abs, nil
}

func (r *Registry) EnsurePlan(repo string) (Plan, bool, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.ensurePlanLocked(repo)
}

func (r *Registry) ensurePlanLocked(repo string) (Plan, bool, error) {
	canonical, err := canonicalRepo(repo)
	if err != nil {
		return Plan{}, false, err
	}
	for _, item := range r.planListLocked() {
		if item.Repo != "" && samePath(item.Repo, canonical) {
			return item, false, nil
		}
	}
	if err := os.MkdirAll(r.plansRoot, 0o700); err != nil {
		return Plan{}, false, err
	}
	planID, planDir, err := allocatePlanDir(r.plansRoot)
	if err != nil {
		return Plan{}, false, err
	}
	if err := os.MkdirAll(filepath.Join(planDir, "plan", "items"), 0o700); err != nil {
		return Plan{}, false, err
	}
	name := filepath.Base(canonical)
	if err := os.WriteFile(filepath.Join(planDir, "plan.md"), []byte("# "+name+"\n"), 0o600); err != nil {
		return Plan{}, false, err
	}
	if err := os.WriteFile(filepath.Join(planDir, "NOTES.md"), nil, 0o600); err != nil {
		return Plan{}, false, err
	}
	if err := writePlanRepo(planDir, canonical); err != nil {
		return Plan{}, false, err
	}
	return Plan{ID: planID, Name: name, Repo: canonical}, true, nil
}

func (r *Registry) planListLocked() []Plan {
	values, err := ListPlans(r.plansRoot)
	if err != nil {
		return []Plan{}
	}
	return values
}

func (r *Registry) planRepos() []string {
	values, err := ListPlans(r.plansRoot)
	if err != nil {
		return nil
	}
	repos := make([]string, 0, len(values))
	for _, value := range values {
		if value.Repo != "" {
			repos = append(repos, filepath.Clean(value.Repo))
		}
	}
	return repos
}

func allocatePlanDir(root string) (string, string, error) {
	for attempt := 0; attempt < 8; attempt++ {
		random := make([]byte, 8)
		if _, err := rand.Read(random); err != nil {
			return "", "", err
		}
		id := hex.EncodeToString(random)
		dir := filepath.Join(root, id)
		if err := os.Mkdir(dir, 0o700); err == nil {
			return id, dir, nil
		} else if !os.IsExist(err) {
			return "", "", err
		}
	}
	return "", "", fmt.Errorf("could not allocate a unique plan id")
}

func samePath(left, right string) bool {
	return strings.EqualFold(filepath.Clean(left), filepath.Clean(right))
}
