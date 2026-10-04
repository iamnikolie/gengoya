package jobs

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestIDForOperation(t *testing.T) {
	assert.Equal(t, "abc123", IDForOperation("models/veo-3.1-generate-preview/operations/abc123"))
	assert.Equal(t, "abc123", IDForOperation("operations/abc123"))
	assert.Equal(t, "abc123", IDForOperation("abc123"))
	assert.Equal(t, "abc-1_2", IDForOperation("models/m/operations/abc-1_2"))
	// Path traversal and separators cannot survive into a filename.
	assert.Equal(t, "etcpasswd", IDForOperation("../../etc/passwd/../..//etcpasswd"))
	assert.NotContains(t, IDForOperation("../.."), "/")
	assert.NotEmpty(t, IDForOperation(""))
}

func TestSaveLoadRoundTrip(t *testing.T) {
	dir := t.TempDir()
	created := time.Now().UTC().Truncate(time.Second)
	j := Job{
		Operation:       "models/veo-3.1-fast-generate-preview/operations/xyz789",
		Provider:        "gemini",
		Model:           "veo-3.1-fast",
		APIID:           "veo-3.1-fast-generate-preview",
		Prompt:          "a fox in snow",
		Resolution:      "720p",
		Duration:        "8",
		Aspect:          "16:9",
		OutDir:          "/tmp/out",
		PosterFrames:    4,
		CostEstimateUSD: 0.80,
		CreatedAt:       created,
		State:           StatePending,
	}
	require.NoError(t, Save(dir, j))

	got, err := Load(dir, "xyz789")
	require.NoError(t, err)
	assert.Equal(t, "xyz789", got.ID, "id is derived from the operation when unset")
	assert.Equal(t, j.Operation, got.Operation)
	assert.Equal(t, j.Prompt, got.Prompt)
	assert.InDelta(t, 0.80, got.CostEstimateUSD, 1e-9)
	assert.True(t, created.Equal(got.CreatedAt))

	// Job files hold prompts; they must not be world-readable.
	info, err := os.Stat(filepath.Join(Dir(dir), "xyz789.json"))
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(0600), info.Mode().Perm())
}

func TestLoadMissing(t *testing.T) {
	_, err := Load(t.TempDir(), "nope")
	assert.ErrorContains(t, err, "no job")
}

func TestResolveByIDAndOperation(t *testing.T) {
	dir := t.TempDir()
	op := "models/veo-3.1-generate-preview/operations/opid42"
	require.NoError(t, Save(dir, Job{Operation: op, State: StatePending}))

	byID, err := Resolve(dir, "opid42")
	require.NoError(t, err)
	assert.Equal(t, op, byID.Operation)

	byOp, err := Resolve(dir, op)
	require.NoError(t, err)
	assert.Equal(t, "opid42", byOp.ID)

	_, err = Resolve(dir, "models/m/operations/unknown")
	assert.ErrorContains(t, err, "unknown job")
}

func TestListNewestFirst(t *testing.T) {
	dir := t.TempDir()
	now := time.Now()
	require.NoError(t, Save(dir, Job{Operation: "op/old", CreatedAt: now.Add(-time.Hour), State: StateFetched}))
	require.NoError(t, Save(dir, Job{Operation: "op/new", CreatedAt: now, State: StatePending}))

	all, err := List(dir)
	require.NoError(t, err)
	require.Len(t, all, 2)
	assert.Equal(t, "new", all[0].ID)
	assert.Equal(t, "old", all[1].ID)
}

func TestListEmptyAndUnwritten(t *testing.T) {
	all, err := List(t.TempDir())
	require.NoError(t, err)
	assert.Empty(t, all)
}

func TestListSkipsGarbage(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.MkdirAll(Dir(dir), 0700))
	require.NoError(t, os.WriteFile(filepath.Join(Dir(dir), "broken.json"), []byte("not json"), 0600))
	require.NoError(t, os.WriteFile(filepath.Join(Dir(dir), "notes.txt"), []byte("ignored"), 0600))
	require.NoError(t, Save(dir, Job{Operation: "op/good", CreatedAt: time.Now()}))

	all, err := List(dir)
	require.NoError(t, err)
	require.Len(t, all, 1)
	assert.Equal(t, "good", all[0].ID)
}

func TestSaveUpdatesState(t *testing.T) {
	dir := t.TempDir()
	j := Job{Operation: "op/s1", State: StatePending, CreatedAt: time.Now()}
	require.NoError(t, Save(dir, j))

	j.ID = IDForOperation(j.Operation)
	j.State = StateFetched
	j.Paths = []string{"/tmp/a.mp4", "/tmp/a-poster.png"}
	require.NoError(t, Save(dir, j))

	got, err := Load(dir, "s1")
	require.NoError(t, err)
	assert.Equal(t, StateFetched, got.State)
	assert.Len(t, got.Paths, 2)
}

func TestIDForOperationLongIDsStayDistinct(t *testing.T) {
	// Omni interaction ids are long with a shared head; ids must not collide.
	head := "v1_ChdZbTNDYXE2V0JhRHBuc0VQcXNTR2lRRRIXWW0zQ2FxNldCYURwbnNFUHFz"
	a := IDForOperation("interactions/" + head + "AAAA")
	b := IDForOperation("interactions/" + head + "BBBB")
	assert.NotEqual(t, a, b)
	assert.LessOrEqual(t, len(a), 64)
	assert.Equal(t, a, IDForOperation("interactions/"+head+"AAAA"))
}
