// Package jobs persists long-running video jobs so a detached run can be
// polled and fetched later from another process.
package jobs

import (
	"crypto/sha1"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
	"unicode"
)

// Job states.
const (
	StatePending = "pending"
	StateDone    = "done"
	StateFetched = "fetched"
	StateFailed  = "failed"
)

// Job is one detached video generation, stored as JSON in the profile dir.
type Job struct {
	ID        string `json:"id"`
	Operation string `json:"operation"`
	Provider  string `json:"provider"`
	Model     string `json:"model"`  // registry alias
	APIID     string `json:"api_id"` // provider model id
	Prompt    string `json:"prompt"`

	Resolution string `json:"resolution,omitempty"`
	Duration   string `json:"duration_seconds,omitempty"`
	Aspect     string `json:"aspect,omitempty"`

	OutDir       string `json:"out_dir"`
	Name         string `json:"name,omitempty"`
	PosterFrames int    `json:"poster_frames"`

	CostEstimateUSD float64   `json:"cost_estimate_usd"`
	CostUSD         float64   `json:"cost_usd,omitempty"`    // actual, from API usage (Omni)
	CostSource      string    `json:"cost_source,omitempty"` // "usage" when CostUSD is set
	CreatedAt       time.Time `json:"created_at"`
	State           string    `json:"state"`
	Paths           []string  `json:"paths,omitempty"`
	Error           string    `json:"error,omitempty"`
}

// Dir returns the jobs directory inside a profile directory.
func Dir(profileDir string) string {
	return filepath.Join(profileDir, "jobs")
}

// IDForOperation derives a short, filesystem-safe job id from an operation name
// such as "models/veo-3.1-generate-preview/operations/abc123".
func IDForOperation(operation string) string {
	op := strings.TrimSpace(operation)
	op = strings.TrimSuffix(op, "/")
	if i := strings.LastIndex(op, "/"); i >= 0 {
		op = op[i+1:]
	}
	var b strings.Builder
	for _, r := range op {
		if unicode.IsLetter(r) || unicode.IsDigit(r) || r == '-' || r == '_' {
			b.WriteRune(r)
		}
	}
	id := b.String()
	if id == "" {
		return fmt.Sprintf("job-%d", time.Now().UnixNano())
	}
	if len(id) > 64 {
		// Omni interaction ids are ~60+ chars with a shared random-looking
		// prefix; truncating could collide, so keep a head plus a hash.
		sum := sha1.Sum([]byte(id))
		id = id[:24] + "-" + hex.EncodeToString(sum[:])[:12]
	}
	return id
}

func path(profileDir, id string) string {
	return filepath.Join(Dir(profileDir), id+".json")
}

// Save writes the job record (0600 in a 0700 directory).
func Save(profileDir string, j Job) error {
	if j.ID == "" {
		j.ID = IDForOperation(j.Operation)
	}
	if err := os.MkdirAll(Dir(profileDir), 0700); err != nil {
		return fmt.Errorf("jobs.Save: %w", err)
	}
	data, err := json.MarshalIndent(j, "", "  ")
	if err != nil {
		return fmt.Errorf("jobs.Save: %w", err)
	}
	if err := os.WriteFile(path(profileDir, j.ID), data, 0600); err != nil {
		return fmt.Errorf("jobs.Save: %w", err)
	}
	return nil
}

// Load reads one job by id.
func Load(profileDir, id string) (Job, error) {
	data, err := os.ReadFile(path(profileDir, id))
	if err != nil {
		if os.IsNotExist(err) {
			return Job{}, fmt.Errorf("jobs.Load: no job %q — run 'gengoya jobs list'", id)
		}
		return Job{}, fmt.Errorf("jobs.Load: %w", err)
	}
	var j Job
	if err := json.Unmarshal(data, &j); err != nil {
		return Job{}, fmt.Errorf("jobs.Load: %w", err)
	}
	return j, nil
}

// Resolve accepts either a job id or a full operation name.
func Resolve(profileDir, ref string) (Job, error) {
	if j, err := Load(profileDir, ref); err == nil {
		return j, nil
	}
	id := IDForOperation(ref)
	j, err := Load(profileDir, id)
	if err != nil {
		return Job{}, fmt.Errorf("jobs.Resolve: unknown job %q", ref)
	}
	return j, nil
}

// List returns all stored jobs, newest first.
func List(profileDir string) ([]Job, error) {
	entries, err := os.ReadDir(Dir(profileDir))
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("jobs.List: %w", err)
	}
	var out []Job
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		j, err := Load(profileDir, strings.TrimSuffix(e.Name(), ".json"))
		if err != nil {
			continue
		}
		out = append(out, j)
	}
	sort.Slice(out, func(i, k int) bool { return out[i].CreatedAt.After(out[k].CreatedAt) })
	return out, nil
}
