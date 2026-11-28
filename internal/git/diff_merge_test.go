package git

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	sdkprovider "github.com/oakwood-commons/scafctl-plugin-sdk/provider"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func writeRepoFile(t *testing.T, repoPath, relPath string, content []byte) {
	t.Helper()
	fullPath := filepath.Join(repoPath, filepath.FromSlash(relPath))
	require.NoError(t, os.MkdirAll(filepath.Dir(fullPath), 0o700))
	require.NoError(t, os.WriteFile(fullPath, content, 0o600))
}

func gitOutput(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...) //nolint:gosec // G204: test helper with static args
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	require.NoError(t, err, "git %v failed: %s", args, string(out))
	return strings.TrimSpace(string(out))
}

func TestExecuteProvider_Diff(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git command not available")
	}

	p := NewPlugin()
	desc, err := p.GetProviderDescriptor(context.Background(), "git")
	require.NoError(t, err)
	validator := sdkprovider.NewSchemaValidator()

	tests := []struct {
		name               string
		setup              func(t *testing.T) (repoPath string, inputs map[string]any)
		wantFiles          []map[string]any
		wantChangedFiles   int
		wantTotalAdditions int
		wantTotalDeletions int
		wantOutputSubstr   string
	}{
		{
			name: "three-dot diff between diverged base and head uses merge-base",
			setup: func(t *testing.T) (string, map[string]any) {
				repoPath := t.TempDir()
				runTestGit(t, repoPath, "init", "-b", "main")
				writeRepoFile(t, repoPath, "a.txt", []byte("line1\nline2\n"))
				writeRepoFile(t, repoPath, "b.txt", []byte("keep1\nkeep2\n"))
				runTestGit(t, repoPath, "add", ".")
				runTestGit(t, repoPath, "commit", "-m", "initial")

				runTestGit(t, repoPath, "checkout", "-b", "feature")
				writeRepoFile(t, repoPath, "a.txt", []byte("line1\nmodified\nextra\n"))
				writeRepoFile(t, repoPath, "added.txt", []byte("new1\nnew2\nnew3\n"))
				runTestGit(t, repoPath, "add", ".")
				runTestGit(t, repoPath, "commit", "-m", "feature changes")

				// Advance main after branching; three-dot diff must ignore main_only.txt.
				runTestGit(t, repoPath, "checkout", "main")
				writeRepoFile(t, repoPath, "main_only.txt", []byte("m1\nm2\nm3\nm4\nm5\n"))
				runTestGit(t, repoPath, "add", ".")
				runTestGit(t, repoPath, "commit", "-m", "main advance")

				return repoPath, map[string]any{
					"operation": "diff",
					"path":      repoPath,
					"base":      "main",
					"head":      "feature",
				}
			},
			wantFiles: []map[string]any{
				{"path": "a.txt", "additions": 2, "deletions": 1, "binary": false},
				{"path": "added.txt", "additions": 3, "deletions": 0, "binary": false},
			},
			wantChangedFiles:   2,
			wantTotalAdditions: 5,
			wantTotalDeletions: 1,
			wantOutputSubstr:   "2\t1\ta.txt",
		},
		{
			name: "two-dot diff when three_dot is false compares trees directly",
			setup: func(t *testing.T) (string, map[string]any) {
				repoPath := t.TempDir()
				runTestGit(t, repoPath, "init", "-b", "main")
				writeRepoFile(t, repoPath, "a.txt", []byte("line1\n"))
				runTestGit(t, repoPath, "add", ".")
				runTestGit(t, repoPath, "commit", "-m", "initial")

				runTestGit(t, repoPath, "checkout", "-b", "feature")
				writeRepoFile(t, repoPath, "feature.txt", []byte("f1\nf2\n"))
				runTestGit(t, repoPath, "add", ".")
				runTestGit(t, repoPath, "commit", "-m", "feature")

				runTestGit(t, repoPath, "checkout", "main")
				writeRepoFile(t, repoPath, "main_only.txt", []byte("m1\nm2\nm3\nm4\n"))
				runTestGit(t, repoPath, "add", ".")
				runTestGit(t, repoPath, "commit", "-m", "main advance")

				return repoPath, map[string]any{
					"operation": "diff",
					"path":      repoPath,
					"base":      "main",
					"head":      "feature",
					"three_dot": false,
				}
			},
			wantFiles: []map[string]any{
				{"path": "feature.txt", "additions": 2, "deletions": 0, "binary": false},
				{"path": "main_only.txt", "additions": 0, "deletions": 4, "binary": false},
			},
			wantChangedFiles:   2,
			wantTotalAdditions: 2,
			wantTotalDeletions: 4,
			wantOutputSubstr:   "0\t4\tmain_only.txt",
		},
		{
			name: "head defaults to HEAD when base is set",
			setup: func(t *testing.T) (string, map[string]any) {
				repoPath := t.TempDir()
				runTestGit(t, repoPath, "init", "-b", "main")
				writeRepoFile(t, repoPath, "doc.md", []byte("title\n"))
				runTestGit(t, repoPath, "add", ".")
				runTestGit(t, repoPath, "commit", "-m", "first")

				writeRepoFile(t, repoPath, "doc.md", []byte("title\nbody\n"))
				runTestGit(t, repoPath, "add", ".")
				runTestGit(t, repoPath, "commit", "-m", "second")

				return repoPath, map[string]any{
					"operation": "diff",
					"path":      repoPath,
					"base":      "HEAD~1",
				}
			},
			wantFiles: []map[string]any{
				{"path": "doc.md", "additions": 1, "deletions": 0, "binary": false},
			},
			wantChangedFiles:   1,
			wantTotalAdditions: 1,
			wantTotalDeletions: 0,
			wantOutputSubstr:   "1\t0\tdoc.md",
		},
		{
			name: "working-tree diff against HEAD when base and head are omitted",
			setup: func(t *testing.T) (string, map[string]any) {
				repoPath := t.TempDir()
				runTestGit(t, repoPath, "init", "-b", "main")
				writeRepoFile(t, repoPath, "tracked.txt", []byte("one\ntwo\n"))
				runTestGit(t, repoPath, "add", ".")
				runTestGit(t, repoPath, "commit", "-m", "initial")

				// Unstaged modification + staged new file.
				writeRepoFile(t, repoPath, "tracked.txt", []byte("one\ntwo-edited\nthree\n"))
				writeRepoFile(t, repoPath, "staged.txt", []byte("staged\n"))
				runTestGit(t, repoPath, "add", "staged.txt")

				return repoPath, map[string]any{
					"operation": "diff",
					"path":      repoPath,
				}
			},
			wantFiles: []map[string]any{
				{"path": "staged.txt", "additions": 1, "deletions": 0, "binary": false},
				{"path": "tracked.txt", "additions": 2, "deletions": 1, "binary": false},
			},
			wantChangedFiles:   2,
			wantTotalAdditions: 3,
			wantTotalDeletions: 1,
		},
		{
			name: "working-tree diff against explicit head when base is omitted",
			setup: func(t *testing.T) (string, map[string]any) {
				repoPath := t.TempDir()
				runTestGit(t, repoPath, "init", "-b", "main")
				writeRepoFile(t, repoPath, "f.txt", []byte("a\n"))
				runTestGit(t, repoPath, "add", ".")
				runTestGit(t, repoPath, "commit", "-m", "first")

				writeRepoFile(t, repoPath, "f.txt", []byte("a\nb\n"))
				runTestGit(t, repoPath, "add", ".")
				runTestGit(t, repoPath, "commit", "-m", "second")

				writeRepoFile(t, repoPath, "f.txt", []byte("a\nb\nc\n"))

				return repoPath, map[string]any{
					"operation": "diff",
					"path":      repoPath,
					"head":      "HEAD~1",
				}
			},
			wantFiles: []map[string]any{
				{"path": "f.txt", "additions": 2, "deletions": 0, "binary": false},
			},
			wantChangedFiles:   1,
			wantTotalAdditions: 2,
			wantTotalDeletions: 0,
		},
		{
			name: "binary file sets binary true and zero line counts alongside text changes",
			setup: func(t *testing.T) (string, map[string]any) {
				repoPath := t.TempDir()
				runTestGit(t, repoPath, "init", "-b", "main")
				writeRepoFile(t, repoPath, "readme.txt", []byte("hello\n"))
				runTestGit(t, repoPath, "add", ".")
				runTestGit(t, repoPath, "commit", "-m", "initial")

				writeRepoFile(t, repoPath, "asset.bin", []byte{0x89, 'P', 'N', 'G', 0x00, 0x01, 0x02, 0xff})
				writeRepoFile(t, repoPath, "readme.txt", []byte("hello\nworld\n!\n"))
				runTestGit(t, repoPath, "add", ".")
				runTestGit(t, repoPath, "commit", "-m", "add binary and text")

				return repoPath, map[string]any{
					"operation": "diff",
					"path":      repoPath,
					"base":      "HEAD~1",
					"head":      "HEAD",
				}
			},
			wantFiles: []map[string]any{
				{"path": "asset.bin", "additions": 0, "deletions": 0, "binary": true},
				{"path": "readme.txt", "additions": 2, "deletions": 0, "binary": false},
			},
			wantChangedFiles:   2,
			wantTotalAdditions: 2,
			wantTotalDeletions: 0,
			wantOutputSubstr:   "-\t-\tasset.bin",
		},
		{
			name: "renamed file with spaces records path and oldPath",
			setup: func(t *testing.T) (string, map[string]any) {
				repoPath := t.TempDir()
				runTestGit(t, repoPath, "init", "-b", "main")
				writeRepoFile(t, repoPath, "dir one/old file.txt", []byte("l1\nl2\nl3\nl4\nl5\n"))
				runTestGit(t, repoPath, "add", ".")
				runTestGit(t, repoPath, "commit", "-m", "initial")

				require.NoError(t, os.MkdirAll(filepath.Join(repoPath, "dir two"), 0o700))
				runTestGit(t, repoPath, "mv", "dir one/old file.txt", "dir two/new file.txt")
				writeRepoFile(t, repoPath, "dir two/new file.txt", []byte("l1\nl2\nl3\nl4\nl5\nl6\n"))
				runTestGit(t, repoPath, "add", ".")
				runTestGit(t, repoPath, "commit", "-m", "rename file")

				return repoPath, map[string]any{
					"operation": "diff",
					"path":      repoPath,
					"base":      "HEAD~1",
					"head":      "HEAD",
				}
			},
			wantFiles: []map[string]any{
				{
					"path":      "dir two/new file.txt",
					"oldPath":   "dir one/old file.txt",
					"additions": 1,
					"deletions": 0,
					"binary":    false,
				},
			},
			wantChangedFiles:   1,
			wantTotalAdditions: 1,
			wantTotalDeletions: 0,
			wantOutputSubstr:   "dir one/old file.txt => dir two/new file.txt",
		},
		{
			name: "clean working tree returns non-nil empty files slice",
			setup: func(t *testing.T) (string, map[string]any) {
				repoPath := t.TempDir()
				runTestGit(t, repoPath, "init", "-b", "main")
				writeRepoFile(t, repoPath, "clean.txt", []byte("ok\n"))
				runTestGit(t, repoPath, "add", ".")
				runTestGit(t, repoPath, "commit", "-m", "initial")

				return repoPath, map[string]any{
					"operation": "diff",
					"path":      repoPath,
				}
			},
			wantFiles:          []map[string]any{},
			wantChangedFiles:   0,
			wantTotalAdditions: 0,
			wantTotalDeletions: 0,
		},
		{
			name: "empty unborn repository returns empty files slice",
			setup: func(t *testing.T) (string, map[string]any) {
				repoPath := t.TempDir()
				runTestGit(t, repoPath, "init", "-b", "main")

				return repoPath, map[string]any{
					"operation": "diff",
					"path":      repoPath,
				}
			},
			wantFiles:          []map[string]any{},
			wantChangedFiles:   0,
			wantTotalAdditions: 0,
			wantTotalDeletions: 0,
		},
		{
			name: "working-tree diff against explicit tree revision when base is omitted",
			setup: func(t *testing.T) (string, map[string]any) {
				repoPath := t.TempDir()
				runTestGit(t, repoPath, "init", "-b", "main")
				writeRepoFile(t, repoPath, "f.txt", []byte("a\n"))
				runTestGit(t, repoPath, "add", ".")
				runTestGit(t, repoPath, "commit", "-m", "first")

				writeRepoFile(t, repoPath, "f.txt", []byte("a\nb\n"))
				runTestGit(t, repoPath, "add", ".")
				runTestGit(t, repoPath, "commit", "-m", "second")

				writeRepoFile(t, repoPath, "f.txt", []byte("a\nb\nc\n"))

				return repoPath, map[string]any{
					"operation": "diff",
					"path":      repoPath,
					"head":      "HEAD^1^{tree}",
				}
			},
			wantFiles: []map[string]any{
				{"path": "f.txt", "additions": 2, "deletions": 0, "binary": false},
			},
			wantChangedFiles:   1,
			wantTotalAdditions: 2,
			wantTotalDeletions: 0,
		},
		{
			name: "unborn repository with staged file returns staged additions",
			setup: func(t *testing.T) (string, map[string]any) {
				repoPath := t.TempDir()
				runTestGit(t, repoPath, "init", "-b", "main")
				writeRepoFile(t, repoPath, "first.txt", []byte("alpha\nbeta\n"))
				runTestGit(t, repoPath, "add", "first.txt")

				return repoPath, map[string]any{
					"operation": "diff",
					"path":      repoPath,
				}
			},
			wantFiles: []map[string]any{
				{"path": "first.txt", "additions": 2, "deletions": 0, "binary": false},
			},
			wantChangedFiles:   1,
			wantTotalAdditions: 2,
			wantTotalDeletions: 0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			repoPath, inputs := tt.setup(t)

			result, execErr := p.ExecuteProvider(context.Background(), "git", inputs)
			require.NoError(t, execErr)
			require.NotNil(t, result)

			data, ok := result.Data.(map[string]any)
			require.True(t, ok)
			assert.Equal(t, true, data["success"])
			assert.Equal(t, "", data["error"])
			assert.Equal(t, "diff", data["operation"])
			assert.Equal(t, repoPath, data["path"])
			assert.Equal(t, tt.wantChangedFiles, data["changedFiles"])
			assert.Equal(t, tt.wantTotalAdditions, data["totalAdditions"])
			assert.Equal(t, tt.wantTotalDeletions, data["totalDeletions"])

			gotFiles, ok := data["files"].([]map[string]any)
			require.True(t, ok, "files must be a non-nil []map[string]any")
			assert.NotNil(t, gotFiles)
			assert.Equal(t, tt.wantFiles, gotFiles)

			if tt.wantOutputSubstr != "" {
				assert.Contains(t, data["output"], tt.wantOutputSubstr)
			}

			encoded, marshalErr := json.Marshal(result)
			require.NoError(t, marshalErr)
			if len(tt.wantFiles) == 0 {
				assert.Contains(t, string(encoded), `"files":[]`)
			}

			for _, cap := range []sdkprovider.Capability{sdkprovider.CapabilityFrom, sdkprovider.CapabilityAction} {
				require.NoError(t, validator.ValidateOutput(data, desc.OutputSchemas[cap]), "output schema validation failed for %s", cap)
			}
		})
	}
}

func TestExecuteProvider_Diff_DefaultPath(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git command not available")
	}

	repoPath := t.TempDir()
	runTestGit(t, repoPath, "init", "-b", "main")
	writeRepoFile(t, repoPath, "hello.txt", []byte("v1\n"))
	runTestGit(t, repoPath, "add", ".")
	runTestGit(t, repoPath, "commit", "-m", "first")
	writeRepoFile(t, repoPath, "hello.txt", []byte("v1\nv2\n"))

	t.Chdir(repoPath)

	p := NewPlugin()
	result, err := p.ExecuteProvider(context.Background(), "git", map[string]any{
		"operation": "diff",
	})
	require.NoError(t, err)
	data := result.Data.(map[string]any)
	assert.Equal(t, ".", data["path"])
	assert.Equal(t, 1, data["changedFiles"])
	assert.Equal(t, 1, data["totalAdditions"])
}

func TestExecuteProvider_Diff_Errors(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git command not available")
	}

	repoPath := t.TempDir()
	runTestGit(t, repoPath, "init", "-b", "main")
	runTestGit(t, repoPath, "commit", "--allow-empty", "-m", "initial")

	regularFile := filepath.Join(t.TempDir(), "not-a-dir.txt")
	require.NoError(t, os.WriteFile(regularFile, []byte("x"), 0o600))

	tests := []struct {
		name    string
		inputs  map[string]any
		wantErr string
	}{
		{
			name: "leading hyphen base rejected",
			inputs: map[string]any{
				"operation": "diff",
				"path":      repoPath,
				"base":      "--output=/tmp/injected",
			},
			wantErr: "must not start with '-'",
		},
		{
			name: "leading hyphen head rejected",
			inputs: map[string]any{
				"operation": "diff",
				"path":      repoPath,
				"base":      "HEAD",
				"head":      "--ext-diff",
			},
			wantErr: "must not start with '-'",
		},
		{
			name: "range operator in base rejected",
			inputs: map[string]any{
				"operation": "diff",
				"path":      repoPath,
				"base":      "HEAD~1..HEAD",
			},
			wantErr: "range operator",
		},
		{
			name: "commit-parent revision-set syntax in head-only diff rejected",
			inputs: map[string]any{
				"operation": "diff",
				"path":      repoPath,
				"head":      "HEAD^!",
			},
			wantErr: "revision-set syntax",
		},
		{
			name: "all-parents revision-set syntax in head-only diff rejected",
			inputs: map[string]any{
				"operation": "diff",
				"path":      repoPath,
				"head":      "HEAD^@",
			},
			wantErr: "revision-set syntax",
		},
		{
			name: "nth-parent range shorthand in head-only diff rejected",
			inputs: map[string]any{
				"operation": "diff",
				"path":      repoPath,
				"head":      "HEAD^-",
			},
			wantErr: "revision-set syntax",
		},
		{
			name: "numbered nth-parent range shorthand in head-only diff rejected",
			inputs: map[string]any{
				"operation": "diff",
				"path":      repoPath,
				"head":      "HEAD^-1",
			},
			wantErr: "revision-set syntax",
		},
		{
			name: "caret-prefixed negative revision in head-only diff rejected",
			inputs: map[string]any{
				"operation": "diff",
				"path":      repoPath,
				"head":      "^HEAD",
			},
			wantErr: "revision-set syntax",
		},
		{
			name: "range operator in head-only diff rejected",
			inputs: map[string]any{
				"operation": "diff",
				"path":      repoPath,
				"head":      "HEAD~1..HEAD",
			},
			wantErr: "range operator",
		},
		{
			name: "whitespace in ref rejected",
			inputs: map[string]any{
				"operation": "diff",
				"path":      repoPath,
				"base":      "main branch",
			},
			wantErr: "whitespace or control characters",
		},
		{
			name: "control byte in ref rejected",
			inputs: map[string]any{
				"operation": "diff",
				"path":      repoPath,
				"head":      "HEAD\x00--flag",
			},
			wantErr: "whitespace or control characters",
		},
		{
			name: "non-string base rejected",
			inputs: map[string]any{
				"operation": "diff",
				"path":      repoPath,
				"base":      42,
			},
			wantErr: "base must be a string",
		},
		{
			name: "non-string head rejected",
			inputs: map[string]any{
				"operation": "diff",
				"path":      repoPath,
				"head":      42,
			},
			wantErr: "head must be a string",
		},
		{
			name: "non-boolean three_dot rejected",
			inputs: map[string]any{
				"operation": "diff",
				"path":      repoPath,
				"base":      "HEAD",
				"three_dot": "true",
			},
			wantErr: "three_dot must be a boolean",
		},
		{
			name: "non-string path rejected",
			inputs: map[string]any{
				"operation": "diff",
				"path":      123,
			},
			wantErr: "path must be a string",
		},
		{
			name: "nonexistent directory",
			inputs: map[string]any{
				"operation": "diff",
				"path":      filepath.Join(t.TempDir(), "missing"),
			},
			wantErr: "directory does not exist",
		},
		{
			name: "file path instead of directory",
			inputs: map[string]any{
				"operation": "diff",
				"path":      regularFile,
			},
			wantErr: "path is not a directory",
		},
		{
			name: "not a git repository",
			inputs: map[string]any{
				"operation": "diff",
				"path":      t.TempDir(),
			},
			wantErr: "computing diff",
		},
		{
			name: "unknown revision",
			inputs: map[string]any{
				"operation": "diff",
				"path":      repoPath,
				"base":      "refs/heads/nonexistent",
				"head":      "HEAD",
			},
			wantErr: "computing diff",
		},
	}

	p := NewPlugin()
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := p.ExecuteProvider(context.Background(), "git", tt.inputs)
			require.Error(t, err)
			assert.Contains(t, err.Error(), tt.wantErr)
		})
	}
}

func TestExecuteProvider_Diff_CanceledContext(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git command not available")
	}

	repoPath := t.TempDir()
	runTestGit(t, repoPath, "init", "-b", "main")
	runTestGit(t, repoPath, "commit", "--allow-empty", "-m", "initial")

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	p := NewPlugin()
	_, err := p.ExecuteProvider(ctx, "git", map[string]any{
		"operation": "diff",
		"path":      repoPath,
		"base":      "HEAD",
	})
	require.Error(t, err)
	assert.ErrorIs(t, err, context.Canceled)
}

func TestExecuteProvider_MergeCheck(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git command not available")
	}

	p := NewPlugin()
	desc, err := p.GetProviderDescriptor(context.Background(), "git")
	require.NoError(t, err)
	validator := sdkprovider.NewSchemaValidator()

	tests := []struct {
		name                string
		setup               func(t *testing.T) (repoPath string, inputs map[string]any)
		wantClean           bool
		wantConflictedFiles []string
		wantOutputSubstr    string
	}{
		{
			name: "clean 3-way merge returns treeOid and empty conflictedFiles",
			setup: func(t *testing.T) (string, map[string]any) {
				repoPath := t.TempDir()
				runTestGit(t, repoPath, "init", "-b", "main")
				writeRepoFile(t, repoPath, "shared.txt", []byte("base\n"))
				runTestGit(t, repoPath, "add", ".")
				runTestGit(t, repoPath, "commit", "-m", "base")

				runTestGit(t, repoPath, "checkout", "-b", "feature")
				writeRepoFile(t, repoPath, "feature_only.txt", []byte("from feature\n"))
				runTestGit(t, repoPath, "add", ".")
				runTestGit(t, repoPath, "commit", "-m", "feature")

				runTestGit(t, repoPath, "checkout", "main")
				writeRepoFile(t, repoPath, "main_only.txt", []byte("from main\n"))
				runTestGit(t, repoPath, "add", ".")
				runTestGit(t, repoPath, "commit", "-m", "main")

				return repoPath, map[string]any{
					"operation": "merge-check",
					"path":      repoPath,
					"base":      "main",
					"head":      "feature",
				}
			},
			wantClean:           true,
			wantConflictedFiles: []string{},
		},
		{
			name: "conflicting edits return clean false and sorted conflictedFiles without dirtying status",
			setup: func(t *testing.T) (string, map[string]any) {
				repoPath := t.TempDir()
				runTestGit(t, repoPath, "init", "-b", "main")
				writeRepoFile(t, repoPath, "sub dir/conflict file.txt", []byte("original\n"))
				writeRepoFile(t, repoPath, "alpha.txt", []byte("original-alpha\n"))
				runTestGit(t, repoPath, "add", ".")
				runTestGit(t, repoPath, "commit", "-m", "base")

				runTestGit(t, repoPath, "checkout", "-b", "feature")
				writeRepoFile(t, repoPath, "sub dir/conflict file.txt", []byte("feature-edit\n"))
				writeRepoFile(t, repoPath, "alpha.txt", []byte("feature-alpha\n"))
				runTestGit(t, repoPath, "add", ".")
				runTestGit(t, repoPath, "commit", "-m", "feature")

				runTestGit(t, repoPath, "checkout", "main")
				writeRepoFile(t, repoPath, "sub dir/conflict file.txt", []byte("main-edit\n"))
				writeRepoFile(t, repoPath, "alpha.txt", []byte("main-alpha\n"))
				runTestGit(t, repoPath, "add", ".")
				runTestGit(t, repoPath, "commit", "-m", "main")

				// Checkout feature and omit head to verify default head="HEAD".
				runTestGit(t, repoPath, "checkout", "feature")

				return repoPath, map[string]any{
					"operation": "merge-check",
					"path":      repoPath,
					"base":      "main",
				}
			},
			wantClean:           false,
			wantConflictedFiles: []string{"alpha.txt", "sub dir/conflict file.txt"},
			wantOutputSubstr:    "CONFLICT",
		},
		{
			name: "modify-delete conflict returns clean false and conflicted file",
			setup: func(t *testing.T) (string, map[string]any) {
				repoPath := t.TempDir()
				runTestGit(t, repoPath, "init", "-b", "main")
				writeRepoFile(t, repoPath, " doomed.txt", []byte("v1\n"))
				runTestGit(t, repoPath, "add", ".")
				runTestGit(t, repoPath, "commit", "-m", "base")

				runTestGit(t, repoPath, "checkout", "-b", "feature")
				writeRepoFile(t, repoPath, " doomed.txt", []byte("v2-modified\n"))
				runTestGit(t, repoPath, "add", ".")
				runTestGit(t, repoPath, "commit", "-m", "modify")

				runTestGit(t, repoPath, "checkout", "main")
				runTestGit(t, repoPath, "rm", " doomed.txt")
				runTestGit(t, repoPath, "commit", "-m", "delete")

				return repoPath, map[string]any{
					"operation": "merge-check",
					"path":      repoPath,
					"base":      "main",
					"head":      "feature",
				}
			},
			wantClean:           false,
			wantConflictedFiles: []string{" doomed.txt"},
			wantOutputSubstr:    "CONFLICT",
		},
		{
			name: "binary file conflict returns clean false and conflicted binary path",
			setup: func(t *testing.T) (string, map[string]any) {
				repoPath := t.TempDir()
				runTestGit(t, repoPath, "init", "-b", "main")
				writeRepoFile(t, repoPath, "logo.bin", []byte{0x00, 0x01, 0x02})
				runTestGit(t, repoPath, "add", ".")
				runTestGit(t, repoPath, "commit", "-m", "base")

				runTestGit(t, repoPath, "checkout", "-b", "feature")
				writeRepoFile(t, repoPath, "logo.bin", []byte{0x00, 0xaa, 0xbb})
				runTestGit(t, repoPath, "add", ".")
				runTestGit(t, repoPath, "commit", "-m", "feature binary")

				runTestGit(t, repoPath, "checkout", "main")
				writeRepoFile(t, repoPath, "logo.bin", []byte{0x00, 0xcc, 0xdd})
				runTestGit(t, repoPath, "add", ".")
				runTestGit(t, repoPath, "commit", "-m", "main binary")

				return repoPath, map[string]any{
					"operation": "merge-check",
					"path":      repoPath,
					"base":      "main",
					"head":      "feature",
				}
			},
			wantClean:           false,
			wantConflictedFiles: []string{"logo.bin"},
			wantOutputSubstr:    "CONFLICT",
		},
		{
			name: "clean same-file auto-merge returns merged treeOid without conflict",
			setup: func(t *testing.T) (string, map[string]any) {
				repoPath := t.TempDir()
				runTestGit(t, repoPath, "init", "-b", "main")
				writeRepoFile(t, repoPath, "shared.txt", []byte("top\nl1\nl2\nl3\nl4\nl5\nbottom\n"))
				writeRepoFile(t, repoPath, "feature_only.txt", []byte("init\n"))
				writeRepoFile(t, repoPath, "main_only.txt", []byte("init\n"))
				runTestGit(t, repoPath, "add", ".")
				runTestGit(t, repoPath, "commit", "-m", "base")

				runTestGit(t, repoPath, "checkout", "-b", "feature")
				writeRepoFile(t, repoPath, "shared.txt", []byte("top-feature\nl1\nl2\nl3\nl4\nl5\nbottom\n"))
				runTestGit(t, repoPath, "add", ".")
				runTestGit(t, repoPath, "commit", "-m", "feature top edit")

				runTestGit(t, repoPath, "checkout", "main")
				writeRepoFile(t, repoPath, "shared.txt", []byte("top\nl1\nl2\nl3\nl4\nl5\nbottom-main\n"))
				runTestGit(t, repoPath, "add", ".")
				runTestGit(t, repoPath, "commit", "-m", "main bottom edit")

				return repoPath, map[string]any{
					"operation": "merge-check",
					"path":      repoPath,
					"base":      "main",
					"head":      "feature",
				}
			},
			wantClean:           true,
			wantConflictedFiles: []string{},
		},
		{
			name: "multi-path rename-rename conflict parses 3-path informational tuple cleanly",
			setup: func(t *testing.T) (string, map[string]any) {
				repoPath := t.TempDir()
				runTestGit(t, repoPath, "init", "-b", "main")
				writeRepoFile(t, repoPath, "orig.txt", []byte("same1\nsame2\nsame3\nsame4\n"))
				runTestGit(t, repoPath, "add", ".")
				runTestGit(t, repoPath, "commit", "-m", "base")

				runTestGit(t, repoPath, "checkout", "-b", "feature")
				runTestGit(t, repoPath, "mv", "orig.txt", "renamed_feature.txt")
				runTestGit(t, repoPath, "commit", "-m", "rename feature")

				runTestGit(t, repoPath, "checkout", "main")
				runTestGit(t, repoPath, "mv", "orig.txt", "renamed_main.txt")
				runTestGit(t, repoPath, "commit", "-m", "rename main")

				return repoPath, map[string]any{
					"operation": "merge-check",
					"path":      repoPath,
					"base":      "main",
					"head":      "feature",
				}
			},
			wantClean:           false,
			wantConflictedFiles: []string{"orig.txt", "renamed_feature.txt", "renamed_main.txt"},
			wantOutputSubstr:    "CONFLICT (rename/rename)",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			repoPath, inputs := tt.setup(t)

			statusBefore := gitOutput(t, repoPath, "status", "--porcelain")
			stagedBefore := gitOutput(t, repoPath, "diff", "--cached")

			result, execErr := p.ExecuteProvider(context.Background(), "git", inputs)
			require.NoError(t, execErr)
			require.NotNil(t, result)

			statusAfter := gitOutput(t, repoPath, "status", "--porcelain")
			stagedAfter := gitOutput(t, repoPath, "diff", "--cached")
			assert.Equal(t, statusBefore, statusAfter, "merge-check must not mutate working tree status")
			assert.Equal(t, stagedBefore, stagedAfter, "merge-check must not mutate index")

			data, ok := result.Data.(map[string]any)
			require.True(t, ok)
			assert.Equal(t, true, data["success"])
			assert.Equal(t, "", data["error"])
			assert.Equal(t, "merge-check", data["operation"])
			assert.Equal(t, repoPath, data["path"])
			assert.Equal(t, tt.wantClean, data["clean"])

			gotConflicts, ok := data["conflictedFiles"].([]string)
			require.True(t, ok, "conflictedFiles must be a non-nil []string")
			assert.NotNil(t, gotConflicts)
			assert.Equal(t, tt.wantConflictedFiles, gotConflicts)

			treeOID, _ := data["treeOid"].(string)
			if tt.wantClean {
				require.NotEmpty(t, treeOID)
				objType := gitOutput(t, repoPath, "cat-file", "-t", treeOID)
				assert.Equal(t, "tree", objType)
				lsTree := gitOutput(t, repoPath, "ls-tree", "--name-only", treeOID)
				assert.Contains(t, lsTree, "feature_only.txt")
				assert.Contains(t, lsTree, "main_only.txt")
			} else {
				assert.Empty(t, treeOID)
			}

			if tt.wantOutputSubstr != "" {
				outStr, _ := data["output"].(string)
				assert.Contains(t, outStr, tt.wantOutputSubstr)
				for _, line := range strings.Split(outStr, "\n") {
					assert.NotEqual(t, "1", line, "raw tuple path count must not leak into output")
					assert.NotEqual(t, "2", line, "raw tuple path count must not leak into output")
					assert.NotEqual(t, "3", line, "raw tuple path count must not leak into output")
					assert.NotEqual(t, "Auto-merging", line, "raw tuple type header must not leak into output")
					assert.NotEqual(t, "CONFLICT (content)", line, "raw tuple type header must not leak into output")
				}
			}

			encoded, marshalErr := json.Marshal(result)
			require.NoError(t, marshalErr)
			if len(tt.wantConflictedFiles) == 0 {
				assert.Contains(t, string(encoded), `"conflictedFiles":[]`)
			}

			for _, cap := range []sdkprovider.Capability{sdkprovider.CapabilityFrom, sdkprovider.CapabilityAction} {
				require.NoError(t, validator.ValidateOutput(data, desc.OutputSchemas[cap]), "output schema validation failed for %s", cap)
			}
		})
	}
}

func TestExecuteProvider_MergeCheck_DefaultPath(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git command not available")
	}

	repoPath := t.TempDir()
	runTestGit(t, repoPath, "init", "-b", "main")
	writeRepoFile(t, repoPath, "f.txt", []byte("base\n"))
	runTestGit(t, repoPath, "add", ".")
	runTestGit(t, repoPath, "commit", "-m", "base")
	runTestGit(t, repoPath, "checkout", "-b", "feature")
	writeRepoFile(t, repoPath, "g.txt", []byte("feat\n"))
	runTestGit(t, repoPath, "add", ".")
	runTestGit(t, repoPath, "commit", "-m", "feat")

	t.Chdir(repoPath)

	p := NewPlugin()
	result, err := p.ExecuteProvider(context.Background(), "git", map[string]any{
		"operation": "merge-check",
		"base":      "main",
	})
	require.NoError(t, err)
	data := result.Data.(map[string]any)
	assert.Equal(t, ".", data["path"])
	assert.Equal(t, true, data["clean"])
	assert.NotEmpty(t, data["treeOid"])
}

func TestExecuteProvider_WorkingDirectoryFromContext(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git command not available")
	}

	nonRepoCWD := t.TempDir()
	t.Chdir(nonRepoCWD)

	workspaceDir := t.TempDir()
	repoPath := filepath.Join(workspaceDir, "subrepo")
	require.NoError(t, os.MkdirAll(repoPath, 0o700))
	runTestGit(t, repoPath, "init", "-b", "main")
	writeRepoFile(t, repoPath, "app.go", []byte("package app\n"))
	runTestGit(t, repoPath, "add", ".")
	runTestGit(t, repoPath, "commit", "-m", "base")

	runTestGit(t, repoPath, "checkout", "-b", "feature")
	writeRepoFile(t, repoPath, "app.go", []byte("package app\n\nfunc Run() {}\n"))
	runTestGit(t, repoPath, "add", ".")
	runTestGit(t, repoPath, "commit", "-m", "feature")

	p := NewPlugin()

	// Default path "." resolves to repoPath via WorkingDirectoryFromContext.
	repoCtx := sdkprovider.WithWorkingDirectory(context.Background(), repoPath)
	diffRes, err := p.ExecuteProvider(repoCtx, "git", map[string]any{
		"operation": "diff",
		"base":      "main",
	})
	require.NoError(t, err)
	diffData := diffRes.Data.(map[string]any)
	assert.Equal(t, repoPath, diffData["path"])
	assert.Equal(t, 1, diffData["changedFiles"])
	assert.Equal(t, 2, diffData["totalAdditions"])

	// Relative path "subrepo" resolves against workspaceDir via WorkingDirectoryFromContext.
	wsCtx := sdkprovider.WithWorkingDirectory(context.Background(), workspaceDir)
	mergeRes, err := p.ExecuteProvider(wsCtx, "git", map[string]any{
		"operation": "merge-check",
		"path":      "subrepo",
		"base":      "main",
		"head":      "feature",
	})
	require.NoError(t, err)
	mergeData := mergeRes.Data.(map[string]any)
	assert.Equal(t, repoPath, mergeData["path"])
	assert.Equal(t, true, mergeData["clean"])
	assert.NotEmpty(t, mergeData["treeOid"])

	// DryRun and DescribeWhatIf also honor WorkingDirectoryFromContext.
	dryCtx := sdkprovider.WithDryRun(repoCtx, true)
	dryRes, err := p.ExecuteProvider(dryCtx, "git", map[string]any{
		"operation": "diff",
	})
	require.NoError(t, err)
	assert.Equal(t, repoPath, dryRes.Data.(map[string]any)["path"])

	whatIfMsg, err := p.DescribeWhatIf(repoCtx, "git", map[string]any{
		"operation": "merge-check",
		"base":      "main",
	})
	require.NoError(t, err)
	assert.Contains(t, whatIfMsg, repoPath)
}

func TestExecuteProvider_MergeCheck_Errors(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git command not available")
	}

	repoPath := t.TempDir()
	runTestGit(t, repoPath, "init", "-b", "main")
	runTestGit(t, repoPath, "commit", "--allow-empty", "-m", "initial")

	tests := []struct {
		name    string
		inputs  map[string]any
		wantErr string
	}{
		{
			name: "missing base ref",
			inputs: map[string]any{
				"operation": "merge-check",
				"path":      repoPath,
			},
			wantErr: "base is required for merge-check operation",
		},
		{
			name: "leading hyphen base rejected",
			inputs: map[string]any{
				"operation": "merge-check",
				"path":      repoPath,
				"base":      "--trivial-merge",
			},
			wantErr: "must not start with '-'",
		},
		{
			name: "leading hyphen head rejected",
			inputs: map[string]any{
				"operation": "merge-check",
				"path":      repoPath,
				"base":      "main",
				"head":      "--stdin",
			},
			wantErr: "must not start with '-'",
		},
		{
			name: "revision-set syntax in base rejected",
			inputs: map[string]any{
				"operation": "merge-check",
				"path":      repoPath,
				"base":      "main^!",
			},
			wantErr: "revision-set syntax",
		},
		{
			name: "revision-set syntax in head rejected",
			inputs: map[string]any{
				"operation": "merge-check",
				"path":      repoPath,
				"base":      "main",
				"head":      "HEAD^@",
			},
			wantErr: "revision-set syntax",
		},
		{
			name: "caret-prefixed revision in head rejected",
			inputs: map[string]any{
				"operation": "merge-check",
				"path":      repoPath,
				"base":      "main",
				"head":      "^HEAD",
			},
			wantErr: "revision-set syntax",
		},
		{
			name: "non-string base rejected",
			inputs: map[string]any{
				"operation": "merge-check",
				"path":      repoPath,
				"base":      true,
			},
			wantErr: "base must be a string",
		},
		{
			name: "non-string head rejected",
			inputs: map[string]any{
				"operation": "merge-check",
				"path":      repoPath,
				"base":      "main",
				"head":      99,
			},
			wantErr: "head must be a string",
		},
		{
			name: "nonexistent directory",
			inputs: map[string]any{
				"operation": "merge-check",
				"path":      filepath.Join(t.TempDir(), "missing"),
				"base":      "main",
			},
			wantErr: "directory does not exist",
		},
		{
			name: "not a git repository",
			inputs: map[string]any{
				"operation": "merge-check",
				"path":      t.TempDir(),
				"base":      "main",
			},
			wantErr: "checking merge",
		},
		{
			name: "invalid base ref",
			inputs: map[string]any{
				"operation": "merge-check",
				"path":      repoPath,
				"base":      "refs/heads/does-not-exist",
			},
			wantErr: "checking merge",
		},
	}

	p := NewPlugin()
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := p.ExecuteProvider(context.Background(), "git", tt.inputs)
			require.Error(t, err)
			assert.Contains(t, err.Error(), tt.wantErr)
		})
	}
}

func TestExecuteProvider_MergeCheck_CanceledContext(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git command not available")
	}

	repoPath := t.TempDir()
	runTestGit(t, repoPath, "init", "-b", "main")
	runTestGit(t, repoPath, "commit", "--allow-empty", "-m", "initial")

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	p := NewPlugin()
	_, err := p.ExecuteProvider(ctx, "git", map[string]any{
		"operation": "merge-check",
		"path":      repoPath,
		"base":      "main",
	})
	require.Error(t, err)
	assert.ErrorIs(t, err, context.Canceled)
}

func TestCheckMinimumGitVersion(t *testing.T) {
	tests := []struct {
		name    string
		raw     string
		wantErr string
	}{
		{name: "exact minimum 2.38.0", raw: "git version 2.38.0"},
		{name: "newer 2.51.1", raw: "git version 2.51.1"},
		{name: "apple git suffix", raw: "git version 2.39.5 (Apple Git-154)"},
		{name: "windows git suffix", raw: "git version 2.47.0.windows.2"},
		{name: "major 3.0", raw: "git version 3.0.0"},
		{name: "unparseable output ignored", raw: "custom-git-build"},
		{name: "too old 2.37.3", raw: "git version 2.37.3", wantErr: "requires Git 2.38 or newer (found 2.37.3)"},
		{name: "too old 1.8.3.1", raw: "git version 1.8.3.1", wantErr: "requires Git 2.38 or newer (found 1.8.3.1)"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := checkMinimumGitVersion(tt.raw, 2, 38)
			if tt.wantErr == "" {
				assert.NoError(t, err)
			} else {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.wantErr)
			}
		})
	}

	assert.True(t, isUnsupportedMergeTreeFlagError("error: unknown option `write-tree'"))
	assert.True(t, isUnsupportedMergeTreeFlagError("usage: git merge-tree <base-tree> <branch1> <branch2>"))
	assert.False(t, isUnsupportedMergeTreeFlagError("fatal: Not a valid object name main"))
}

func TestParseNumstatZ_MalformedAndEdgeCases(t *testing.T) {
	_, _, _, _, err := parseNumstatZ([]byte("no-tabs-here\x00"))
	require.Error(t, err)

	_, _, _, _, err = parseNumstatZ([]byte("1\tmissing-second-tab\x00"))
	require.Error(t, err)

	_, _, _, _, err = parseNumstatZ([]byte("1\t2\tunterminated"))
	require.Error(t, err)

	_, _, _, _, err = parseNumstatZ([]byte("1\t2\t\x00only-old-path"))
	require.Error(t, err)

	_, _, _, _, err = parseNumstatZ([]byte("1\t2\t\x00old.txt\x00missing-end"))
	require.Error(t, err)

	_, _, _, _, err = parseNumstatZ([]byte("bad\t2\tfile.txt\x00"))
	require.Error(t, err)

	_, _, _, _, err = parseNumstatZ([]byte("1\tbad\tfile.txt\x00"))
	require.Error(t, err)
}

func TestParseMergeTreeZ_EdgeCases(t *testing.T) {
	_, _, _, err := parseMergeTreeZ(nil, true)
	require.Error(t, err)

	_, _, _, err = parseMergeTreeZ([]byte("\x00"), true)
	require.Error(t, err)

	// Contaminated stdout before the tree OID (for example from a verbose custom merge driver) is rejected.
	_, _, _, err = parseMergeTreeZ([]byte("merging\n0123456789abcdef0123456789abcdef01234567\x00"), true)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "invalid tree OID")

	oid, conflicts, out, err := parseMergeTreeZ(
		[]byte("0123456789abcdef0123456789abcdef01234567\x001\x00a.txt\x00Auto-merging\x00Auto-merging a.txt\n\x00"),
		true,
	)
	require.NoError(t, err)
	assert.Equal(t, "0123456789abcdef0123456789abcdef01234567", oid)
	assert.Empty(t, conflicts)
	assert.Equal(t, "Auto-merging a.txt", out)

	oid, conflicts, out, err = parseMergeTreeZ(
		[]byte("0123456789abcdef0123456789abcdef01234567\x00100644 aaaaa 1\tfile.txt\x00100644 bbbbb 2\tfile.txt\x00"),
		false,
	)
	require.NoError(t, err)
	assert.Empty(t, oid)
	assert.Equal(t, []string{"file.txt"}, conflicts)
	assert.Equal(t, "merge conflict in file.txt", out)

	// Conflict with no stage entries includes paths from CONFLICT tuple.
	oid, conflicts, out, err = parseMergeTreeZ(
		[]byte("0123456789abcdef0123456789abcdef01234567\x00\x002\x00dir/b.txt\x00dir/a.txt\x00CONFLICT (file/directory)\x00CONFLICT (file/directory): directory in the way\n\x00"),
		false,
	)
	require.NoError(t, err)
	assert.Empty(t, oid)
	assert.Equal(t, []string{"dir/a.txt", "dir/b.txt"}, conflicts)
	assert.Equal(t, "CONFLICT (file/directory): directory in the way", out)

	// Mixed stage conflict records and tuple-only CONFLICT paths union all unique paths.
	oid, conflicts, out, err = parseMergeTreeZ(
		[]byte("0123456789abcdef0123456789abcdef01234567\x00"+
			"100644 aaaaa 2\tfile.txt\x00"+
			"100644 bbbbb 3\tfile.txt\x00"+
			"\x00"+
			"1\x00file.txt\x00CONFLICT (content)\x00CONFLICT (content): Merge conflict in file.txt\n\x00"+
			"2\x00dir/nested.txt\x00dir\x00CONFLICT (file/directory)\x00CONFLICT (file/directory): directory in the way of dir\n\x00"),
		false,
	)
	require.NoError(t, err)
	assert.Empty(t, oid)
	assert.Equal(t, []string{"dir", "dir/nested.txt", "file.txt"}, conflicts)
	assert.Equal(t, "CONFLICT (content): Merge conflict in file.txt\nCONFLICT (file/directory): directory in the way of dir", out)
}

func TestExecuteProvider_DiffAndMergeCheck_LinkedWorktreeAndBareRepo(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git command not available")
	}

	originRepo := t.TempDir()
	runTestGit(t, originRepo, "init", "-b", "main")
	writeRepoFile(t, originRepo, "base.txt", []byte("one\n"))
	runTestGit(t, originRepo, "add", ".")
	runTestGit(t, originRepo, "commit", "-m", "initial")

	linkedWorktree := filepath.Join(t.TempDir(), "wt")
	runTestGit(t, originRepo, "worktree", "add", "-b", "feature", linkedWorktree, "main")
	writeRepoFile(t, linkedWorktree, "base.txt", []byte("one\ntwo\n"))
	runTestGit(t, linkedWorktree, "add", ".")
	runTestGit(t, linkedWorktree, "commit", "-m", "feature commit")

	// Unstaged edit in linked worktree must be visible to working-tree diff.
	writeRepoFile(t, linkedWorktree, "worktree_local.txt", []byte("dirty\n"))
	runTestGit(t, linkedWorktree, "add", "worktree_local.txt")

	p := NewPlugin()
	wtDiff, err := p.ExecuteProvider(context.Background(), "git", map[string]any{
		"operation": "diff",
		"path":      linkedWorktree,
		"head":      "main",
	})
	require.NoError(t, err)
	wtDiffData := wtDiff.Data.(map[string]any)
	assert.Equal(t, 2, wtDiffData["changedFiles"])
	assert.Equal(t, 2, wtDiffData["totalAdditions"])

	// Bare clone supports ref-to-ref diff and merge-check without a worktree.
	bareRepo := filepath.Join(t.TempDir(), "bare.git")
	runTestGit(t, originRepo, "clone", "--bare", originRepo, bareRepo)

	bareDiff, err := p.ExecuteProvider(context.Background(), "git", map[string]any{
		"operation": "diff",
		"path":      bareRepo,
		"base":      "main",
		"head":      "feature",
	})
	require.NoError(t, err)
	assert.Equal(t, 1, bareDiff.Data.(map[string]any)["changedFiles"])

	bareMerge, err := p.ExecuteProvider(context.Background(), "git", map[string]any{
		"operation": "merge-check",
		"path":      bareRepo,
		"base":      "main",
		"head":      "feature",
	})
	require.NoError(t, err)
	assert.Equal(t, true, bareMerge.Data.(map[string]any)["clean"])
}

func TestExecuteProvider_ReftableBackend(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git command not available")
	}

	repoPath := t.TempDir()
	initCmd := exec.Command("git", "init", "--ref-format=reftable", "-b", "main")
	initCmd.Dir = repoPath
	if out, err := initCmd.CombinedOutput(); err != nil {
		lower := strings.ToLower(string(out))
		if strings.Contains(lower, "unknown option") || strings.Contains(lower, "ref-format") {
			t.Skipf("git binary does not support --ref-format=reftable: %s", strings.TrimSpace(string(out)))
		}
		require.NoError(t, err, "git init --ref-format=reftable failed: %s", string(out))
	}
	runTestGit(t, repoPath, "config", "user.email", "test@example.com")
	runTestGit(t, repoPath, "config", "user.name", "Test User")

	writeRepoFile(t, repoPath, "shared.txt", []byte("line1\n"))
	runTestGit(t, repoPath, "add", ".")
	runTestGit(t, repoPath, "commit", "-m", "initial")

	runTestGit(t, repoPath, "checkout", "-b", "feature")
	writeRepoFile(t, repoPath, "feature.txt", []byte("f1\nf2\n"))
	runTestGit(t, repoPath, "add", ".")
	runTestGit(t, repoPath, "commit", "-m", "feature")

	runTestGit(t, repoPath, "checkout", "main")
	writeRepoFile(t, repoPath, "main.txt", []byte("m1\n"))
	runTestGit(t, repoPath, "add", ".")
	runTestGit(t, repoPath, "commit", "-m", "main advance")

	p := NewPlugin()

	refDiff, err := p.ExecuteProvider(context.Background(), "git", map[string]any{
		"operation": "diff",
		"path":      repoPath,
		"base":      "main",
		"head":      "feature",
	})
	require.NoError(t, err)
	assert.Equal(t, 1, refDiff.Data.(map[string]any)["changedFiles"])
	assert.Equal(t, 2, refDiff.Data.(map[string]any)["totalAdditions"])

	// Working-tree diff against explicit head ref in reftable repository.
	writeRepoFile(t, repoPath, "shared.txt", []byte("line1\nworking-edit\n"))
	wtDiff, err := p.ExecuteProvider(context.Background(), "git", map[string]any{
		"operation": "diff",
		"path":      repoPath,
		"head":      "HEAD~1",
	})
	require.NoError(t, err)
	assert.Equal(t, 2, wtDiff.Data.(map[string]any)["changedFiles"])

	mergeRes, err := p.ExecuteProvider(context.Background(), "git", map[string]any{
		"operation": "merge-check",
		"path":      repoPath,
		"base":      "main",
		"head":      "feature",
	})
	require.NoError(t, err)
	assert.Equal(t, true, mergeRes.Data.(map[string]any)["clean"])
	assert.NotEmpty(t, mergeRes.Data.(map[string]any)["treeOid"])
}

func TestExecuteProvider_SubtreeAndSearchRevisions(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git command not available")
	}

	repoPath := t.TempDir()
	runTestGit(t, repoPath, "init", "-b", "main")
	writeRepoFile(t, repoPath, "subdir/app.txt", []byte("v1\n"))
	runTestGit(t, repoPath, "add", ".")
	runTestGit(t, repoPath, "commit", "-m", "initial_seed_commit")
	runTestGit(t, repoPath, "tag", "-a", "-m", "annotated release", "v1.0.0")

	runTestGit(t, repoPath, "checkout", "-b", "feature")
	writeRepoFile(t, repoPath, "subdir/app.txt", []byte("v1\nv2\nv3\n"))
	runTestGit(t, repoPath, "add", ".")
	runTestGit(t, repoPath, "commit", "-m", "feature_update")

	p := NewPlugin()

	// Commit-message search revision (:/initial_seed_commit) resolves unmodified before peeling.
	searchDiff, err := p.ExecuteProvider(context.Background(), "git", map[string]any{
		"operation": "diff",
		"path":      repoPath,
		"base":      ":/initial_seed_commit",
		"head":      "HEAD",
	})
	require.NoError(t, err)
	assert.Equal(t, 1, searchDiff.Data.(map[string]any)["changedFiles"])
	assert.Equal(t, 2, searchDiff.Data.(map[string]any)["totalAdditions"])

	searchMerge, err := p.ExecuteProvider(context.Background(), "git", map[string]any{
		"operation": "merge-check",
		"path":      repoPath,
		"base":      ":/initial_seed_commit",
		"head":      "feature",
	})
	require.NoError(t, err)
	assert.Equal(t, true, searchMerge.Data.(map[string]any)["clean"])

	// Annotated tag peels cleanly to commit/tree.
	tagMerge, err := p.ExecuteProvider(context.Background(), "git", map[string]any{
		"operation": "merge-check",
		"path":      repoPath,
		"base":      "v1.0.0",
		"head":      "feature",
	})
	require.NoError(t, err)
	assert.Equal(t, true, tagMerge.Data.(map[string]any)["clean"])

	// Subtree revision (HEAD:subdir) resolves directly to a tree object for two-dot and head-only diffs.
	subtreeTwoDot, err := p.ExecuteProvider(context.Background(), "git", map[string]any{
		"operation": "diff",
		"path":      repoPath,
		"base":      "HEAD~1:subdir",
		"head":      "HEAD:subdir",
		"three_dot": false,
	})
	require.NoError(t, err)
	assert.Equal(t, 1, subtreeTwoDot.Data.(map[string]any)["changedFiles"])
	assert.Equal(t, 2, subtreeTwoDot.Data.(map[string]any)["totalAdditions"])

	subtreeDir := filepath.Join(repoPath, "subdir")
	writeRepoFile(t, repoPath, "subdir/app.txt", []byte("v1\nv2\nv3\nv4\n"))
	subtreeHeadOnly, err := p.ExecuteProvider(context.Background(), "git", map[string]any{
		"operation": "diff",
		"path":      subtreeDir,
		"head":      "HEAD:subdir",
	})
	require.NoError(t, err)
	assert.Equal(t, 1, subtreeHeadOnly.Data.(map[string]any)["changedFiles"])

	// Repo-level and inherited GIT_CONFIG_PARAMETERS / GIT_CONFIG_COUNT aliases are overridden by --config-env.
	runTestGit(t, repoPath, "config", "alias."+internalGitAliasName, "!false")
	t.Setenv("GIT_CONFIG_PARAMETERS", "'alias."+internalGitAliasName+"=!false'")
	t.Setenv("GIT_CONFIG_COUNT", "1")
	t.Setenv("GIT_CONFIG_KEY_0", "alias."+internalGitAliasName)
	t.Setenv("GIT_CONFIG_VALUE_0", "!false")

	overrideRes, err := p.ExecuteProvider(context.Background(), "git", map[string]any{
		"operation": "diff",
		"path":      repoPath,
		"base":      "main",
		"head":      "feature",
	})
	require.NoError(t, err)
	assert.Equal(t, 1, overrideRes.Data.(map[string]any)["changedFiles"])

	overrideMergeRes, err := p.ExecuteProvider(context.Background(), "git", map[string]any{
		"operation": "merge-check",
		"path":      repoPath,
		"base":      "main",
		"head":      "feature",
	})
	require.NoError(t, err)
	assert.Equal(t, true, overrideMergeRes.Data.(map[string]any)["clean"])

	// Blob revision (HEAD:subdir/app.txt) is rejected because it cannot peel to a tree or commit.
	_, err = p.ExecuteProvider(context.Background(), "git", map[string]any{
		"operation": "diff",
		"path":      repoPath,
		"head":      "HEAD:subdir/app.txt",
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "computing diff")
}

func TestExecuteProvider_WorktreeSpecificConfigAndRelativeInclude(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git command not available")
	}

	originRepo := t.TempDir()
	runTestGit(t, originRepo, "init", "-b", "main")
	runTestGit(t, originRepo, "config", "extensions.worktreeConfig", "true")

	writeRepoFile(t, originRepo, ".gitattributes", []byte("*.wtb diff=wtbin\n"))
	writeRepoFile(t, originRepo, "sample.wtb", []byte("line1\n"))
	writeRepoFile(t, originRepo, "sample.wtm", []byte("line-base\n"))
	runTestGit(t, originRepo, "add", ".")
	runTestGit(t, originRepo, "commit", "-m", "base")

	runTestGit(t, originRepo, "checkout", "-b", "feature")
	writeRepoFile(t, originRepo, "sample.wtb", []byte("line1\nline2\n"))
	writeRepoFile(t, originRepo, "sample.wtm", []byte("feature-top\nline-base\n"))
	runTestGit(t, originRepo, "add", ".")
	runTestGit(t, originRepo, "commit", "-m", "feature")

	runTestGit(t, originRepo, "checkout", "main")
	writeRepoFile(t, originRepo, "sample.wtm", []byte("line-base\nmain-bottom\n"))
	runTestGit(t, originRepo, "add", ".")
	runTestGit(t, originRepo, "commit", "-m", "main")

	linkedWT := filepath.Join(t.TempDir(), "wt-cfg")
	runTestGit(t, originRepo, "worktree", "add", "--detach", linkedWT, "feature")

	wtGitDir := gitOutput(t, linkedWT, "rev-parse", "--git-dir")
	if !filepath.IsAbs(wtGitDir) {
		wtGitDir = filepath.Join(linkedWT, wtGitDir)
	}
	wtAttrsPath := filepath.Join(wtGitDir, "wt-merge.gitattributes")
	require.NoError(t, os.WriteFile(wtAttrsPath, []byte("*.wtm merge=union\n"), 0o600))
	includedCfg := fmt.Sprintf("[diff \"wtbin\"]\n\tbinary = true\n[core]\n\tattributesFile = %s\n", filepath.ToSlash(wtAttrsPath))
	require.NoError(t, os.WriteFile(filepath.Join(wtGitDir, "extra-wt.gitconfig"), []byte(includedCfg), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(wtGitDir, "config.worktree"), []byte("[include]\n\tpath = extra-wt.gitconfig\n"), 0o600))

	p := NewPlugin()

	// Create a second commit on main that also modifies line-base so originRepo conflicts without merge=union.
	runTestGit(t, originRepo, "checkout", "main")
	writeRepoFile(t, originRepo, "sample.wtm", []byte("main-replaced\n"))
	runTestGit(t, originRepo, "add", "sample.wtm")
	runTestGit(t, originRepo, "commit", "-m", "main conflict without union")

	// In originRepo (without worktree config), sample.wtb is diffed as text and sample.wtm conflicts.
	originDiff, err := p.ExecuteProvider(context.Background(), "git", map[string]any{
		"operation": "diff",
		"path":      originRepo,
		"base":      "main",
		"head":      "feature",
	})
	require.NoError(t, err)
	originFiles := originDiff.Data.(map[string]any)["files"].([]map[string]any)
	require.Len(t, originFiles, 2)
	assert.Equal(t, "sample.wtb", originFiles[0]["path"])
	assert.Equal(t, false, originFiles[0]["binary"])
	assert.Equal(t, 1, originFiles[0]["additions"])

	originMerge, err := p.ExecuteProvider(context.Background(), "git", map[string]any{
		"operation": "merge-check",
		"path":      originRepo,
		"base":      "main",
		"head":      "feature",
	})
	require.NoError(t, err)
	assert.Equal(t, false, originMerge.Data.(map[string]any)["clean"])

	// In linkedWT, config.worktree + relative include marks sample.wtb binary and resolves sample.wtm via built-in union.
	wtDiff, err := p.ExecuteProvider(context.Background(), "git", map[string]any{
		"operation": "diff",
		"path":      linkedWT,
		"base":      "main",
		"head":      "feature",
	})
	require.NoError(t, err)
	wtFiles := wtDiff.Data.(map[string]any)["files"].([]map[string]any)
	require.Len(t, wtFiles, 2)
	assert.Equal(t, "sample.wtb", wtFiles[0]["path"])
	assert.Equal(t, true, wtFiles[0]["binary"])
	assert.Equal(t, 0, wtFiles[0]["additions"])

	wtMerge, err := p.ExecuteProvider(context.Background(), "git", map[string]any{
		"operation": "merge-check",
		"path":      linkedWT,
		"base":      "main",
		"head":      "feature",
	})
	require.NoError(t, err)
	assert.Equal(t, true, wtMerge.Data.(map[string]any)["clean"])
}

func TestExecuteProvider_MergeCheck_ExternalMergeDriverRejected(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git command not available")
	}

	repoPath := t.TempDir()
	runTestGit(t, repoPath, "init", "-b", "main")
	writeRepoFile(t, repoPath, ".gitattributes", []byte("*.uni merge=union\n"))
	writeRepoFile(t, repoPath, "sub/.gitattributes", []byte("*.drv merge=CustomShell\n"))
	writeRepoFile(t, repoPath, "sub/conflict.drv", []byte("base\n"))
	writeRepoFile(t, repoPath, "notes.uni", []byte("base\n"))
	writeRepoFile(t, repoPath, "plain.txt", []byte("base-plain\n"))
	runTestGit(t, repoPath, "add", ".")
	runTestGit(t, repoPath, "commit", "-m", "base")

	// Branch touching only plain.txt and built-in union file notes.uni (does not touch *.drv).
	runTestGit(t, repoPath, "checkout", "-b", "plain-feature")
	writeRepoFile(t, repoPath, "plain.txt", []byte("feature-plain\n"))
	writeRepoFile(t, repoPath, "notes.uni", []byte("feature-note\n"))
	runTestGit(t, repoPath, "add", ".")
	runTestGit(t, repoPath, "commit", "-m", "plain and union edits")

	// Branch touching sub/conflict.drv (mapped to external merge.CustomShell.driver).
	runTestGit(t, repoPath, "checkout", "-b", "driver-feature", "main")
	writeRepoFile(t, repoPath, "sub/conflict.drv", []byte("feature-drv\n"))
	runTestGit(t, repoPath, "add", ".")
	runTestGit(t, repoPath, "commit", "-m", "driver feature")

	runTestGit(t, repoPath, "checkout", "main")
	writeRepoFile(t, repoPath, "notes.uni", []byte("main-note\n"))
	writeRepoFile(t, repoPath, "sub/conflict.drv", []byte("main-drv\n"))
	runTestGit(t, repoPath, "add", ".")
	runTestGit(t, repoPath, "commit", "-m", "main edits")

	runTestGit(t, repoPath, "checkout", "-b", "main-no-drv", "HEAD~1")
	writeRepoFile(t, repoPath, "notes.uni", []byte("main-note\n"))
	runTestGit(t, repoPath, "add", "notes.uni")
	runTestGit(t, repoPath, "commit", "-m", "main union only")

	sentinelPath := filepath.Join(t.TempDir(), "driver-executed.sentinel")
	driverShellCmd := fmt.Sprintf("echo ran > %q", sentinelPath)
	runTestGit(t, repoPath, "config", "merge.CustomShell.name", "external shell driver")
	runTestGit(t, repoPath, "config", "merge.CustomShell.driver", driverShellCmd)
	runTestGit(t, repoPath, "config", "merge.unuseddriver.name", "unused global-style driver")
	runTestGit(t, repoPath, "config", "merge.unuseddriver.driver", driverShellCmd)
	runTestGit(t, repoPath, "config", "core.fsmonitor", driverShellCmd)

	p := NewPlugin()

	// Unused configured external merge drivers do not fail merges whose touched paths only use built-in drivers.
	allowedRes, allowedErr := p.ExecuteProvider(context.Background(), "git", map[string]any{
		"operation": "merge-check",
		"path":      repoPath,
		"base":      "main-no-drv",
		"head":      "plain-feature",
	})
	require.NoError(t, allowedErr)
	assert.Equal(t, true, allowedRes.Data.(map[string]any)["clean"], "built-in union driver must succeed when external merge drivers are unused on touched paths")
	assert.NoFileExists(t, sentinelPath, "unused external merge driver and core.fsmonitor must never execute")

	// Working-tree diff must also suppress core.fsmonitor execution.
	writeRepoFile(t, repoPath, "plain.txt", []byte("dirty-plain\n"))
	_, diffErr := p.ExecuteProvider(context.Background(), "git", map[string]any{
		"operation": "diff",
		"path":      repoPath,
	})
	require.NoError(t, diffErr)
	assert.NoFileExists(t, sentinelPath, "working-tree diff must not execute core.fsmonitor")

	// Touched path resolving to configured external merge.CustomShell.driver is rejected before merge-tree runs,
	// both from the repository root and when invoked from a subdirectory path.
	for _, targetPath := range []string{repoPath, filepath.Join(repoPath, "sub")} {
		_, rejectedErr := p.ExecuteProvider(context.Background(), "git", map[string]any{
			"operation": "merge-check",
			"path":      targetPath,
			"base":      "main",
			"head":      "driver-feature",
		})
		require.Error(t, rejectedErr)
		assert.Contains(t, rejectedErr.Error(), `external merge driver "CustomShell" is not supported in read-only merge-check; only built-in Git merge drivers are evaluated`)
		assert.NoFileExists(t, sentinelPath, "rejected external merge driver shell command must never execute")
	}

	runTestGit(t, repoPath, "config", "--unset", "core.fsmonitor")
	runTestGit(t, repoPath, "checkout", "--", "plain.txt")
	assert.Equal(t, "", gitOutput(t, repoPath, "status", "--porcelain", "-uall"))
}

func TestExecuteProvider_MergeCheck_SourceIndexAndSpecialDriverNames(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git command not available")
	}

	repoPath := t.TempDir()
	runTestGit(t, repoPath, "init", "-b", "main")
	writeRepoFile(t, repoPath, "plain.txt", []byte("base-plain\n"))
	writeRepoFile(t, repoPath, "drv.dat", []byte("base-drv\n"))
	writeRepoFile(t, repoPath, "lit-set.txt", []byte("base\n"))
	writeRepoFile(t, repoPath, "lit-unset.txt", []byte("base\n"))
	writeRepoFile(t, repoPath, "lit-unspec.txt", []byte("base\n"))
	runTestGit(t, repoPath, "add", ".")
	runTestGit(t, repoPath, "commit", "-m", "base without gitattributes")

	// Unrelated checked-out branch with no .gitattributes file in worktree or live index.
	runTestGit(t, repoPath, "checkout", "-b", "third-no-attrs")
	writeRepoFile(t, repoPath, "unrelated.txt", []byte("third\n"))
	runTestGit(t, repoPath, "add", ".")
	runTestGit(t, repoPath, "commit", "-m", "third branch without attributes")

	// Plain-only branches where merge attribute is unspecified.
	runTestGit(t, repoPath, "checkout", "-b", "plain-a", "main")
	writeRepoFile(t, repoPath, "plain.txt", []byte("plain-a\n"))
	runTestGit(t, repoPath, "add", "plain.txt")
	runTestGit(t, repoPath, "commit", "-m", "plain a")

	runTestGit(t, repoPath, "checkout", "-b", "plain-b", "main")
	writeRepoFile(t, repoPath, "plain.txt", []byte("base-plain\nplain-b\n"))
	runTestGit(t, repoPath, "add", "plain.txt")
	runTestGit(t, repoPath, "commit", "-m", "plain b")

	// Merge tips both assign merge=CustomShell while checked-out branch lacks .gitattributes.
	runTestGit(t, repoPath, "checkout", "-b", "tip-a", "main")
	writeRepoFile(t, repoPath, ".gitattributes", []byte("*.dat merge=CustomShell\n"))
	writeRepoFile(t, repoPath, "drv.dat", []byte("tip-a-drv\n"))
	runTestGit(t, repoPath, "add", ".")
	runTestGit(t, repoPath, "commit", "-m", "tip a")

	runTestGit(t, repoPath, "checkout", "-b", "tip-b", "main")
	writeRepoFile(t, repoPath, ".gitattributes", []byte("*.dat merge=CustomShell\n"))
	writeRepoFile(t, repoPath, "drv.dat", []byte("tip-b-drv\n"))
	runTestGit(t, repoPath, "add", ".")
	runTestGit(t, repoPath, "commit", "-m", "tip b")

	// Leave third-no-attrs checked out so worktree and live index contain no .gitattributes.
	runTestGit(t, repoPath, "checkout", "third-no-attrs")

	sentinelPath := filepath.Join(t.TempDir(), "driver.sentinel")
	driverCmd := fmt.Sprintf("echo executed > %q", sentinelPath)
	runTestGit(t, repoPath, "config", "merge.renormalize", "true")
	runTestGit(t, repoPath, "config", "merge.CustomShell.driver", driverCmd)

	p := NewPlugin()

	// Source-index (--cached) probe rejects CustomShell even when checked-out branch has no .gitattributes.
	_, tipErr := p.ExecuteProvider(context.Background(), "git", map[string]any{
		"operation": "merge-check",
		"path":      repoPath,
		"base":      "tip-a",
		"head":      "tip-b",
	})
	require.Error(t, tipErr)
	assert.Contains(t, tipErr.Error(), `external merge driver "CustomShell" is not supported`)
	assert.NoFileExists(t, sentinelPath)

	// Setting GIT_ATTR_SOURCE on Git < 2.41 (where Git ignores GIT_ATTR_SOURCE) must NOT skip
	// the private source-index probes.
	t.Run("git_2_38_attr_source_env", func(t *testing.T) {
		prevVerFn := gitVersionOutputFunc
		gitVersionOutputFunc = func(context.Context) (string, error) {
			return "git version 2.38.4\n", nil
		}
		t.Cleanup(func() { gitVersionOutputFunc = prevVerFn })
		t.Setenv("GIT_ATTR_SOURCE", "HEAD")

		_, pre241Err := p.ExecuteProvider(context.Background(), "git", map[string]any{
			"operation": "merge-check",
			"path":      repoPath,
			"base":      "tip-a",
			"head":      "tip-b",
		})
		require.Error(t, pre241Err)
		assert.Contains(t, pre241Err.Error(), `external merge driver "CustomShell" is not supported`)
		assert.NoFileExists(t, sentinelPath)
	})

	// Unused merge.text.driver must NOT reject merges with unspecified merge attribute when merge.default is absent,
	// whereas explicit merge.default=text (and whitespace-bearing merge.default="custom ") MUST reject.
	runTestGit(t, repoPath, "config", "merge.text.driver", driverCmd)
	absentDefRes, absentDefErr := p.ExecuteProvider(context.Background(), "git", map[string]any{
		"operation": "merge-check",
		"path":      repoPath,
		"base":      "plain-a",
		"head":      "plain-b",
	})
	require.NoError(t, absentDefErr, "absent merge.default must use built-in text merge rather than configured merge.text.driver")
	assert.Equal(t, false, absentDefRes.Data.(map[string]any)["clean"])
	assert.NoFileExists(t, sentinelPath)

	runTestGit(t, repoPath, "config", "merge.default", "text")
	_, explicitDefErr := p.ExecuteProvider(context.Background(), "git", map[string]any{
		"operation": "merge-check",
		"path":      repoPath,
		"base":      "plain-a",
		"head":      "plain-b",
	})
	require.Error(t, explicitDefErr)
	assert.Contains(t, explicitDefErr.Error(), `external merge driver "text" is not supported`)
	assert.NoFileExists(t, sentinelPath)

	runTestGit(t, repoPath, "config", "merge.custom .driver", driverCmd)
	runTestGit(t, repoPath, "config", "merge.default", "custom ")
	_, spaceDefErr := p.ExecuteProvider(context.Background(), "git", map[string]any{
		"operation": "merge-check",
		"path":      repoPath,
		"base":      "plain-a",
		"head":      "plain-b",
	})
	require.Error(t, spaceDefErr)
	assert.Contains(t, spaceDefErr.Error(), `external merge driver "custom " is not supported`)
	assert.NoFileExists(t, sentinelPath)

	runTestGit(t, repoPath, "config", "--unset", "merge.default")
	runTestGit(t, repoPath, "config", "--unset", "merge.custom .driver")
	runTestGit(t, repoPath, "config", "--unset", "merge.text.driver")

	// Literal merge=set, merge=unset, and merge=unspecified driver names are rejected when configured.
	for _, literalName := range []string{"set", "unset", "unspecified"} {
		t.Run("literal_"+literalName, func(t *testing.T) {
			subRepo := t.TempDir()
			runTestGit(t, subRepo, "init", "-b", "main")
			writeRepoFile(t, subRepo, ".gitattributes", []byte("target.txt merge="+literalName+"\n"))
			writeRepoFile(t, subRepo, "target.txt", []byte("base\n"))
			runTestGit(t, subRepo, "add", ".")
			runTestGit(t, subRepo, "commit", "-m", "base")

			runTestGit(t, subRepo, "checkout", "-b", "branch")
			writeRepoFile(t, subRepo, "target.txt", []byte("branch-change\n"))
			runTestGit(t, subRepo, "add", ".")
			runTestGit(t, subRepo, "commit", "-m", "branch")

			runTestGit(t, subRepo, "checkout", "main")
			writeRepoFile(t, subRepo, "target.txt", []byte("main-change\n"))
			runTestGit(t, subRepo, "add", ".")
			runTestGit(t, subRepo, "commit", "-m", "main")

			runTestGit(t, subRepo, "config", "merge."+literalName+".driver", driverCmd)
			_, err := p.ExecuteProvider(context.Background(), "git", map[string]any{
				"operation": "merge-check",
				"path":      subRepo,
				"base":      "main",
				"head":      "branch",
			})
			require.Error(t, err)
			assert.Contains(t, err.Error(), fmt.Sprintf("external merge driver %q is not supported", literalName))
			assert.NoFileExists(t, sentinelPath)
		})
	}

	// Combined attribute stack: root macro [attr]custommacro in merge tips + checked-out sub/.gitattributes
	// rule "*.mtxt custommacro" on a third branch lacking root .gitattributes must reject both external
	// merge drivers and external conversion filters when merge.renormalize=true.
	t.Run("combined_root_macro_and_checked_out_subdir_attr", func(t *testing.T) {
		macroRepo := t.TempDir()
		runTestGit(t, macroRepo, "init", "-b", "main")
		writeRepoFile(t, macroRepo, "sub/macro.mtxt", []byte("base\n"))
		runTestGit(t, macroRepo, "add", ".")
		runTestGit(t, macroRepo, "commit", "-m", "base")

		runTestGit(t, macroRepo, "checkout", "-b", "third-sub-only", "main")
		writeRepoFile(t, macroRepo, "sub/.gitattributes", []byte("*.mtxt custommacro\n"))
		runTestGit(t, macroRepo, "add", ".")
		runTestGit(t, macroRepo, "commit", "-m", "checked-out subdir attributes without root macro")

		runTestGit(t, macroRepo, "checkout", "-b", "macro-tip-a", "main")
		writeRepoFile(t, macroRepo, ".gitattributes", []byte("[attr]custommacro merge=CustomShell filter=macroflt\n"))
		writeRepoFile(t, macroRepo, "sub/macro.mtxt", []byte("tip-a\n"))
		runTestGit(t, macroRepo, "add", ".")
		runTestGit(t, macroRepo, "commit", "-m", "macro tip a")

		runTestGit(t, macroRepo, "checkout", "-b", "macro-tip-b", "main")
		writeRepoFile(t, macroRepo, ".gitattributes", []byte("[attr]custommacro merge=CustomShell filter=macroflt\n"))
		writeRepoFile(t, macroRepo, "sub/macro.mtxt", []byte("tip-b\n"))
		runTestGit(t, macroRepo, "add", ".")
		runTestGit(t, macroRepo, "commit", "-m", "macro tip b")

		runTestGit(t, macroRepo, "checkout", "third-sub-only")
		runTestGit(t, macroRepo, "config", "merge.renormalize", "true")
		runTestGit(t, macroRepo, "config", "merge.CustomShell.driver", driverCmd)

		_, macroDrvErr := p.ExecuteProvider(context.Background(), "git", map[string]any{
			"operation": "merge-check",
			"path":      macroRepo,
			"base":      "macro-tip-a",
			"head":      "macro-tip-b",
		})
		require.Error(t, macroDrvErr)
		assert.Contains(t, macroDrvErr.Error(), `external merge driver "CustomShell" is not supported`)
		assert.NoFileExists(t, sentinelPath)

		runTestGit(t, macroRepo, "config", "--unset", "merge.CustomShell.driver")
		macroFilterCmd := fmt.Sprintf("sh -c 'echo filtered > %q && cat'", sentinelPath)
		runTestGit(t, macroRepo, "config", "filter.macroflt.clean", macroFilterCmd)

		_, macroFltErr := p.ExecuteProvider(context.Background(), "git", map[string]any{
			"operation": "merge-check",
			"path":      macroRepo,
			"base":      "macro-tip-a",
			"head":      "macro-tip-b",
		})
		require.Error(t, macroFltErr)
		assert.Contains(t, macroFltErr.Error(), `external conversion filter "macroflt" is not supported in read-only merge-check`)
		assert.NoFileExists(t, sentinelPath)
	})
}

func TestExecuteProvider_ExternalConversionFiltersRejected(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git command not available")
	}

	repoPath := t.TempDir()
	runTestGit(t, repoPath, "init", "-b", "main")
	writeRepoFile(t, repoPath, ".gitattributes", []byte("*.flt filter=customflt\n"))
	writeRepoFile(t, repoPath, "tracked.flt", []byte("v1\n"))
	writeRepoFile(t, repoPath, "plain.txt", []byte("p1\n"))
	runTestGit(t, repoPath, "add", ".")
	runTestGit(t, repoPath, "commit", "-m", "base")

	runTestGit(t, repoPath, "checkout", "-b", "feature")
	writeRepoFile(t, repoPath, "tracked.flt", []byte("v2-feature\n"))
	runTestGit(t, repoPath, "add", "tracked.flt")
	runTestGit(t, repoPath, "commit", "-m", "feature")

	runTestGit(t, repoPath, "checkout", "main")
	writeRepoFile(t, repoPath, "tracked.flt", []byte("v2-main\n"))
	runTestGit(t, repoPath, "add", "tracked.flt")
	runTestGit(t, repoPath, "commit", "-m", "main")

	// Backdate tracked.flt before refreshing the index (prior to configuring customflt) so it is stat-clean.
	past := time.Now().Add(-5 * time.Second)
	require.NoError(t, os.Chtimes(filepath.Join(repoPath, "tracked.flt"), past, past))
	runTestGit(t, repoPath, "status", "--porcelain")

	sentinelPath := filepath.Join(t.TempDir(), "filter.sentinel")
	filterCmd := fmt.Sprintf("sh -c 'echo filtered > %q && cat'", sentinelPath)
	runTestGit(t, repoPath, "config", "filter.customflt.clean", filterCmd)
	runTestGit(t, repoPath, "config", "filter.customflt.smudge", filterCmd)

	p := NewPlugin()

	// Stat-clean untouched tracked.flt allows working-tree diff on plain.txt without invoking customflt.
	writeRepoFile(t, repoPath, "plain.txt", []byte("p1\np2\n"))
	cleanDiffRes, cleanDiffErr := p.ExecuteProvider(context.Background(), "git", map[string]any{
		"operation": "diff",
		"path":      repoPath,
	})
	require.NoError(t, cleanDiffErr)
	assert.Equal(t, 1, cleanDiffRes.Data.(map[string]any)["changedFiles"])
	assert.NoFileExists(t, sentinelPath)

	// Dirty tracked.flt in working-tree diff is rejected for both default-HEAD and explicit-head diffs.
	writeRepoFile(t, repoPath, "tracked.flt", []byte("dirty-flt\n"))
	_, defaultDiffErr := p.ExecuteProvider(context.Background(), "git", map[string]any{
		"operation": "diff",
		"path":      repoPath,
	})
	require.Error(t, defaultDiffErr)
	assert.Contains(t, defaultDiffErr.Error(), `external conversion filter "customflt" is not supported in read-only diff`)
	assert.NoFileExists(t, sentinelPath)

	_, explicitDiffErr := p.ExecuteProvider(context.Background(), "git", map[string]any{
		"operation": "diff",
		"path":      repoPath,
		"head":      "HEAD",
	})
	require.Error(t, explicitDiffErr)
	assert.Contains(t, explicitDiffErr.Error(), `external conversion filter "customflt" is not supported in read-only diff`)
	assert.NoFileExists(t, sentinelPath)

	// Default merge.renormalize=false allows merge-check without invoking filters; merge.renormalize=true rejects.
	mergeNoRenorm, mergeNoRenormErr := p.ExecuteProvider(context.Background(), "git", map[string]any{
		"operation": "merge-check",
		"path":      repoPath,
		"base":      "main",
		"head":      "feature",
	})
	require.NoError(t, mergeNoRenormErr)
	assert.Equal(t, false, mergeNoRenorm.Data.(map[string]any)["clean"])
	assert.NoFileExists(t, sentinelPath)

	runTestGit(t, repoPath, "config", "merge.renormalize", "true")
	_, mergeRenormErr := p.ExecuteProvider(context.Background(), "git", map[string]any{
		"operation": "merge-check",
		"path":      repoPath,
		"base":      "main",
		"head":      "feature",
	})
	require.Error(t, mergeRenormErr)
	assert.Contains(t, mergeRenormErr.Error(), `external conversion filter "customflt" is not supported in read-only merge-check`)
	assert.NoFileExists(t, sentinelPath)

	// Inherited empty filter override (disabling a repo filter on a racy-clean file while a second
	// active filter triggers candidate probing) plus trailing-space repository directory and
	// relative GIT_OBJECT_DIRECTORY must neither execute the disabled filter nor bypass the guard.
	t.Run("caller_disabled_filter_and_relative_object_dir", func(t *testing.T) {
		spaceRepo := filepath.Join(t.TempDir(), "repo trailing ")
		require.NoError(t, os.MkdirAll(spaceRepo, 0o700))
		runTestGit(t, spaceRepo, "init", "-b", "main")
		writeRepoFile(t, spaceRepo, ".gitattributes", []byte("*.dis filter=disabledflt\n*.act filter=activeflt\n"))
		writeRepoFile(t, spaceRepo, "sub/racy.dis", []byte("racy-v1\n"))
		writeRepoFile(t, spaceRepo, "sub/dirty.act", []byte("act-v1\n"))
		runTestGit(t, spaceRepo, "add", ".")
		runTestGit(t, spaceRepo, "commit", "-m", "initial")

		// Rewrite sub/racy.dis in the same second as the index so diff-files rehashes it if unneutralized,
		// and dirty sub/dirty.act so activeflt must be rejected.
		writeRepoFile(t, spaceRepo, "sub/racy.dis", []byte("racy-v1\n"))
		writeRepoFile(t, spaceRepo, "sub/dirty.act", []byte("act-v2\n"))

		runTestGit(t, spaceRepo, "config", "filter.disabledflt.clean", filterCmd)
		runTestGit(t, spaceRepo, "config", "filter.activeflt.clean", filterCmd)

		linkDir := filepath.Join(t.TempDir(), "link-root")
		require.NoError(t, os.Symlink(spaceRepo, linkDir))
		t.Chdir(linkDir)
		t.Setenv("GIT_OBJECT_DIRECTORY", ".git/objects")
		t.Setenv("GIT_CONFIG_PARAMETERS", "'filter.disabledflt.clean='")

		_, spaceDiffErr := p.ExecuteProvider(context.Background(), "git", map[string]any{
			"operation": "diff",
			"path":      ".",
		})
		require.Error(t, spaceDiffErr)
		assert.Contains(t, spaceDiffErr.Error(), `external conversion filter "activeflt" is not supported in read-only diff`)
		assert.NoFileExists(t, sentinelPath, "neither caller-disabled filter nor active filter may execute during candidate probing")
	})

	// Nonempty clean/process filter commands inherited via GIT_CONFIG_PARAMETERS (including filter
	// names containing "=") must also be neutralized during racy-clean diff-files and diff-index
	// candidate probes before rejection.
	t.Run("nonempty_git_config_parameters_filter_on_racy_clean_file", func(t *testing.T) {
		subSentinel := filepath.Join(t.TempDir(), "param.sentinel")
		subFilterCmd := fmt.Sprintf("sh -c 'echo filtered > %q && cat'", subSentinel)

		paramRepo := t.TempDir()
		runTestGit(t, paramRepo, "init", "-b", "main")
		writeRepoFile(t, paramRepo, ".gitattributes", []byte("*.pflt filter=paramflt\n*.proc filter=procflt\n*.eq filter=a=b\n"))
		writeRepoFile(t, paramRepo, "racy.pflt", []byte("same\n"))
		writeRepoFile(t, paramRepo, "racy.proc", []byte("same\n"))
		writeRepoFile(t, paramRepo, "racy.eq", []byte("same\n"))
		runTestGit(t, paramRepo, "add", ".")
		runTestGit(t, paramRepo, "commit", "-m", "initial")

		// Rewrite all tracked files with identical content in the same second as the index so
		// diff-files and diff-index rehash them as racy-clean entries.
		writeRepoFile(t, paramRepo, "racy.pflt", []byte("same\n"))
		writeRepoFile(t, paramRepo, "racy.proc", []byte("same\n"))
		writeRepoFile(t, paramRepo, "racy.eq", []byte("same\n"))

		t.Setenv(
			"GIT_CONFIG_PARAMETERS",
			appendGitConfigParameters("", [][2]string{
				{"filter.paramflt.clean", subFilterCmd},
				{"filter.procflt.process", subFilterCmd},
				{"filter.a=b.clean", subFilterCmd},
			}),
		)

		_, defErr := p.ExecuteProvider(context.Background(), "git", map[string]any{
			"operation": "diff",
			"path":      paramRepo,
		})
		require.Error(t, defErr)
		assert.Contains(t, defErr.Error(), "is not supported in read-only diff")
		assert.NoFileExists(t, subSentinel, "inherited GIT_CONFIG_PARAMETERS filter must not execute during racy-clean diff-files probe")

		_, headErr := p.ExecuteProvider(context.Background(), "git", map[string]any{
			"operation": "diff",
			"path":      paramRepo,
			"head":      "HEAD",
		})
		require.Error(t, headErr)
		assert.Contains(t, headErr.Error(), "is not supported in read-only diff")
		assert.NoFileExists(t, subSentinel, "inherited GIT_CONFIG_PARAMETERS filter must not execute during racy-clean diff-index probe")
	})

	t.Run("equals_in_inherited_filter_name_on_racy_clean_file", func(t *testing.T) {
		eqSentinel := filepath.Join(t.TempDir(), "eq.sentinel")
		eqFilterCmd := fmt.Sprintf("sh -c 'echo filtered > %q && cat'", eqSentinel)

		eqRepo := t.TempDir()
		runTestGit(t, eqRepo, "init", "-b", "main")
		writeRepoFile(t, eqRepo, ".gitattributes", []byte("*.eq filter=a=b\n"))
		writeRepoFile(t, eqRepo, "only.eq", []byte("same\n"))
		runTestGit(t, eqRepo, "add", ".")
		runTestGit(t, eqRepo, "commit", "-m", "initial")
		writeRepoFile(t, eqRepo, "only.eq", []byte("same\n"))

		t.Setenv(
			"GIT_CONFIG_PARAMETERS",
			appendGitConfigParameters("", [][2]string{
				{"filter.a=b.clean", eqFilterCmd},
				{"filter.a=b.process", eqFilterCmd},
			}),
		)

		_, eqDefErr := p.ExecuteProvider(context.Background(), "git", map[string]any{
			"operation": "diff",
			"path":      eqRepo,
		})
		require.Error(t, eqDefErr)
		assert.Contains(t, eqDefErr.Error(), `external conversion filter "a=b" is not supported in read-only diff`)
		assert.NoFileExists(t, eqSentinel, "inherited =-bearing filter must not execute during racy-clean diff-files probe")

		_, eqHeadErr := p.ExecuteProvider(context.Background(), "git", map[string]any{
			"operation": "diff",
			"path":      eqRepo,
			"head":      "HEAD",
		})
		require.Error(t, eqHeadErr)
		assert.Contains(t, eqHeadErr.Error(), `external conversion filter "a=b" is not supported in read-only diff`)
		assert.NoFileExists(t, eqSentinel, "inherited =-bearing filter must not execute during racy-clean diff-index probe")
	})

	// Working-tree diff in a superproject must not spawn dirty-submodule inspection that executes
	// a submodule-local clean/process filter on a racy-clean file, whether or not the superproject
	// itself defines any conversion filters.
	t.Run("submodule_local_filter_not_executed_during_working_tree_diff", func(t *testing.T) {
		subOrigin := t.TempDir()
		runTestGit(t, subOrigin, "init", "-b", "main")
		writeRepoFile(t, subOrigin, ".gitattributes", []byte("*.sub filter=subflt\n"))
		writeRepoFile(t, subOrigin, "item.sub", []byte("sub-v1\n"))
		runTestGit(t, subOrigin, "add", ".")
		runTestGit(t, subOrigin, "commit", "-m", "sub initial")

		superRepo := t.TempDir()
		runTestGit(t, superRepo, "init", "-b", "main")
		writeRepoFile(t, superRepo, "plain.txt", []byte("super-v1\n"))
		runTestGit(t, superRepo, "add", "plain.txt")
		runTestGit(t, superRepo, "commit", "-m", "super initial")
		runTestGit(t, superRepo, "-c", "protocol.file.allow=always", "submodule", "add", subOrigin, "childmod")
		runTestGit(t, superRepo, "commit", "-m", "add childmod")
		runTestGit(t, superRepo, "config", "submodule.childmod.ignore", "none")

		subWorktree := filepath.Join(superRepo, "childmod")
		runTestGit(t, subWorktree, "config", "filter.subflt.clean", filterCmd)
		runTestGit(t, subWorktree, "config", "filter.subflt.process", filterCmd)
		// Rewrite item.sub in the same second as the submodule index so git status inside childmod would rehash it.
		subIdxInfo, statErr := os.Stat(filepath.Join(superRepo, ".git", "modules", "childmod", "index"))
		require.NoError(t, statErr)
		writeRepoFile(t, subWorktree, "item.sub", []byte("sub-v1\n"))
		require.NoError(t, os.Chtimes(filepath.Join(subWorktree, "item.sub"), subIdxInfo.ModTime(), subIdxInfo.ModTime()))

		writeRepoFile(t, superRepo, "plain.txt", []byte("super-v1\nsuper-v2\n"))

		// Case 1: superproject has zero configured filters (exercises direct working-tree diff).
		noSuperFltRes, noSuperFltErr := p.ExecuteProvider(context.Background(), "git", map[string]any{
			"operation": "diff",
			"path":      superRepo,
		})
		require.NoError(t, noSuperFltErr)
		assert.Equal(t, 1, noSuperFltRes.Data.(map[string]any)["changedFiles"])
		assert.NoFileExists(t, sentinelPath, "working-tree diff must not execute submodule-local filters")

		// Case 2: superproject also defines an unused filter (exercises diff-files and diff-index candidate probes).
		runTestGit(t, superRepo, "config", "filter.unusedsuper.clean", filterCmd)
		withSuperFltRes, withSuperFltErr := p.ExecuteProvider(context.Background(), "git", map[string]any{
			"operation": "diff",
			"path":      superRepo,
			"head":      "HEAD",
		})
		require.NoError(t, withSuperFltErr)
		assert.Equal(t, 1, withSuperFltRes.Data.(map[string]any)["changedFiles"])
		assert.NoFileExists(t, sentinelPath, "candidate probing must not execute submodule-local filters")
	})
}
