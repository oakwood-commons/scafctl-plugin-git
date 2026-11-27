package git

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	sdkplugin "github.com/oakwood-commons/scafctl-plugin-sdk/plugin"
	sdkprovider "github.com/oakwood-commons/scafctl-plugin-sdk/provider"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestGetProviders(t *testing.T) {
	p := NewPlugin()
	names, err := p.GetProviders(context.Background())
	require.NoError(t, err)
	assert.Equal(t, []string{"git"}, names)
}

func TestGetProviderDescriptor(t *testing.T) {
	p := NewPlugin()
	desc, err := p.GetProviderDescriptor(context.Background(), "git")
	require.NoError(t, err)
	require.NotNil(t, desc)

	assert.Equal(t, "git", desc.Name)
	assert.Equal(t, "Git Provider", desc.DisplayName)
	assert.Equal(t, "v1", desc.APIVersion)
	assert.NotNil(t, desc.Version)
	assert.Contains(t, desc.Capabilities, sdkprovider.CapabilityAction)
	assert.Contains(t, desc.Capabilities, sdkprovider.CapabilityFrom)
	assert.NotNil(t, desc.Schema)
	assert.NotNil(t, desc.Schema.Properties)
	assert.Contains(t, desc.Schema.Required, "operation")
	assert.NotNil(t, desc.OutputSchemas[sdkprovider.CapabilityAction])
	assert.NotNil(t, desc.OutputSchemas[sdkprovider.CapabilityFrom])
	require.NoError(t, sdkprovider.ValidateDescriptor(desc))
	for _, cap := range []sdkprovider.Capability{sdkprovider.CapabilityFrom, sdkprovider.CapabilityAction} {
		props := desc.OutputSchemas[cap].Properties
		require.Contains(t, props, "currentBranch")
		assert.Equal(t, "string", props["currentBranch"].Type)
		require.Contains(t, props, "detached")
		assert.Equal(t, "boolean", props["detached"].Type)
		require.Contains(t, props, "branches")
		assert.Equal(t, "array", props["branches"].Type)
		require.NotNil(t, props["branches"].Items)
		assert.Equal(t, "string", props["branches"].Items.Type)
	}
	assert.NotEmpty(t, desc.Examples)
	assert.NotEmpty(t, desc.Tags)
	assert.Contains(t, desc.SensitiveFields, "password")
}

func TestGetProviderDescriptor_Unknown(t *testing.T) {
	p := NewPlugin()
	_, err := p.GetProviderDescriptor(context.Background(), "nope")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "unknown provider")
}

// =============================================================================
// Input validation tests
// =============================================================================

func TestExecuteProvider_UnknownProvider(t *testing.T) {
	p := NewPlugin()
	_, err := p.ExecuteProvider(context.Background(), "nope", map[string]any{"operation": "status"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "unknown provider")
}

func TestExecuteProvider_MissingOperation(t *testing.T) {
	p := NewPlugin()
	_, err := p.ExecuteProvider(context.Background(), "git", map[string]any{})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "operation is required")
}

func TestExecuteProvider_EmptyOperation(t *testing.T) {
	p := NewPlugin()
	_, err := p.ExecuteProvider(context.Background(), "git", map[string]any{"operation": ""})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "operation is required")
}

func TestExecuteProvider_UnsupportedOperation(t *testing.T) {
	p := NewPlugin()
	_, err := p.ExecuteProvider(context.Background(), "git", map[string]any{
		"operation": "invalid-operation",
		"path":      "/tmp/test",
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "unsupported operation")
}

func TestExecuteProvider_Clone_MissingRepository(t *testing.T) {
	p := NewPlugin()
	_, err := p.ExecuteProvider(context.Background(), "git", map[string]any{
		"operation": "clone",
		"path":      "/tmp/test",
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "repository URL is required")
}

func TestExecuteProvider_Clone_MissingPath(t *testing.T) {
	p := NewPlugin()
	_, err := p.ExecuteProvider(context.Background(), "git", map[string]any{
		"operation":  "clone",
		"repository": "https://github.com/user/repo.git",
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "path is required")
}

func TestExecuteProvider_Status_MissingPath(t *testing.T) {
	p := NewPlugin()
	_, err := p.ExecuteProvider(context.Background(), "git", map[string]any{
		"operation": "status",
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "path is required")
}

func TestExecuteProvider_Add_MissingFiles(t *testing.T) {
	p := NewPlugin()
	_, err := p.ExecuteProvider(context.Background(), "git", map[string]any{
		"operation": "add",
		"path":      "/tmp/test",
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "files is required")
}

func TestExecuteProvider_Add_InvalidFiles(t *testing.T) {
	p := NewPlugin()
	_, err := p.ExecuteProvider(context.Background(), "git", map[string]any{
		"operation": "add",
		"path":      "/tmp/test",
		"files":     "not-an-array",
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "files must be an array")
}

func TestExecuteProvider_Add_MissingPath(t *testing.T) {
	p := NewPlugin()
	_, err := p.ExecuteProvider(context.Background(), "git", map[string]any{
		"operation": "add",
		"files":     []any{"README.md"},
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "path")
}

func TestExecuteProvider_Add_StringSlice(t *testing.T) {
	p := NewPlugin()
	_, err := p.ExecuteProvider(context.Background(), "git", map[string]any{
		"operation": "add",
		"path":      "/nonexistent-dir-for-test",
		"files":     []string{"file1.txt", "file2.txt"},
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "directory does not exist")
}

func TestExecuteProvider_Commit_MissingMessage(t *testing.T) {
	p := NewPlugin()
	_, err := p.ExecuteProvider(context.Background(), "git", map[string]any{
		"operation": "commit",
		"path":      "/tmp/test",
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "message is required")
}

func TestExecuteProvider_Commit_MissingPath(t *testing.T) {
	p := NewPlugin()
	_, err := p.ExecuteProvider(context.Background(), "git", map[string]any{
		"operation": "commit",
		"message":   "test commit",
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "path")
}

func TestExecuteProvider_Checkout_MissingBranch(t *testing.T) {
	p := NewPlugin()
	_, err := p.ExecuteProvider(context.Background(), "git", map[string]any{
		"operation": "checkout",
		"path":      "/tmp/test",
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "branch is required")
}

func TestExecuteProvider_Push_MissingPath(t *testing.T) {
	p := NewPlugin()
	_, err := p.ExecuteProvider(context.Background(), "git", map[string]any{
		"operation": "push",
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "path")
}

func TestExecuteProvider_Log_MissingPath(t *testing.T) {
	p := NewPlugin()
	_, err := p.ExecuteProvider(context.Background(), "git", map[string]any{
		"operation": "log",
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "path")
}

func TestExecuteProvider_Tag_MissingPath(t *testing.T) {
	p := NewPlugin()
	_, err := p.ExecuteProvider(context.Background(), "git", map[string]any{
		"operation": "tag",
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "path")
}

func TestExecuteProvider_Pull_MissingPath(t *testing.T) {
	p := NewPlugin()
	_, err := p.ExecuteProvider(context.Background(), "git", map[string]any{
		"operation": "pull",
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "path")
}

func TestExecuteProvider_Branch_MissingPath(t *testing.T) {
	p := NewPlugin()
	_, err := p.ExecuteProvider(context.Background(), "git", map[string]any{
		"operation": "branch",
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "path")
}

func TestExecuteProvider_Remote_NonexistentPath(t *testing.T) {
	p := NewPlugin()
	_, err := p.ExecuteProvider(context.Background(), "git", map[string]any{
		"operation": "remote",
		"path":      "/nonexistent/path/that/does/not/exist",
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "directory does not exist")
}

func TestExecuteProvider_InvalidInputType(t *testing.T) {
	p := NewPlugin()
	// This should not be possible through the SDK interface but test defensive coding
	_, err := p.ExecuteProvider(context.Background(), "nope", nil)
	require.Error(t, err)
}

// =============================================================================
// Real git operation tests (require git to be installed)
// =============================================================================

func setupTestRepo(t *testing.T) string {
	t.Helper()
	tmpDir := t.TempDir()
	repoPath := filepath.Join(tmpDir, "test-repo")
	require.NoError(t, os.MkdirAll(filepath.Join(repoPath, ".git"), 0o750))
	return repoPath
}

func TestExecuteProvider_Status(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git command not available")
	}

	p := NewPlugin()
	repoPath := setupTestRepo(t)

	result, err := p.ExecuteProvider(context.Background(), "git", map[string]any{
		"operation": "status",
		"path":      repoPath,
	})
	require.NoError(t, err)
	require.NotNil(t, result)

	data := result.Data.(map[string]any)
	assert.Equal(t, "status", data["operation"])
	assert.Equal(t, repoPath, data["path"])
	assert.NotNil(t, data["success"])
}

func TestExecuteProvider_InvalidDirectory(t *testing.T) {
	p := NewPlugin()
	_, err := p.ExecuteProvider(context.Background(), "git", map[string]any{
		"operation": "status",
		"path":      "/nonexistent/path/that/does/not/exist",
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "directory does not exist")
}

func TestExecuteProvider_Pull_NonexistentPath(t *testing.T) {
	p := NewPlugin()
	_, err := p.ExecuteProvider(context.Background(), "git", map[string]any{
		"operation": "pull",
		"path":      "/nonexistent",
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "directory does not exist")
}

func TestExecuteProvider_Branch_NonexistentPath(t *testing.T) {
	p := NewPlugin()
	_, err := p.ExecuteProvider(context.Background(), "git", map[string]any{
		"operation": "branch",
		"path":      "/nonexistent",
	})
	require.Error(t, err)
}

func runTestGit(t *testing.T, dir string, args ...string) {
	t.Helper()
	fullArgs := append([]string{
		"-c", "user.name=Test User",
		"-c", "user.email=test@example.com",
		"-c", "commit.gpgsign=false",
		"-c", "tag.gpgSign=false",
	}, args...)
	cmd := exec.Command("git", fullArgs...) //nolint:gosec // G204: test helper with controlled inputs
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	require.NoError(t, err, "git %v failed: %s", args, string(out))
}

func TestExecuteProvider_Branch_ReadOnly(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git command not available")
	}

	tests := []struct {
		name              string
		setup             func(t *testing.T) string
		wantCurrentBranch string
		wantDetached      bool
		wantBranches      []string
		wantOutputSubstr  string
	}{
		{
			name: "checked-out branch with local branches and remote tracking ref",
			setup: func(t *testing.T) string {
				repoPath := t.TempDir()
				runTestGit(t, repoPath, "init", "-b", "main")
				runTestGit(t, repoPath, "commit", "--allow-empty", "-m", "initial")
				runTestGit(t, repoPath, "checkout", "-b", "feat/demo")
				runTestGit(t, repoPath, "update-ref", "refs/remotes/origin/main", "HEAD")
				runTestGit(t, repoPath, "symbolic-ref", "refs/remotes/origin/HEAD", "refs/remotes/origin/main")
				return repoPath
			},
			wantCurrentBranch: "feat/demo",
			wantDetached:      false,
			wantBranches:      []string{"feat/demo", "main"},
			wantOutputSubstr:  "* feat/demo",
		},
		{
			name: "detached HEAD checkout",
			setup: func(t *testing.T) string {
				repoPath := t.TempDir()
				runTestGit(t, repoPath, "init", "-b", "main")
				runTestGit(t, repoPath, "commit", "--allow-empty", "-m", "initial")
				runTestGit(t, repoPath, "branch", "develop")
				runTestGit(t, repoPath, "checkout", "--detach", "HEAD")
				return repoPath
			},
			wantCurrentBranch: "",
			wantDetached:      true,
			wantBranches:      []string{"develop", "main"},
		},
		{
			name: "newly initialized unborn branch",
			setup: func(t *testing.T) string {
				repoPath := t.TempDir()
				runTestGit(t, repoPath, "init", "-b", "main")
				return repoPath
			},
			wantCurrentBranch: "main",
			wantDetached:      false,
			wantBranches:      []string{},
		},
		{
			name: "linked git worktree target",
			setup: func(t *testing.T) string {
				primaryPath := t.TempDir()
				runTestGit(t, primaryPath, "init", "-b", "main")
				runTestGit(t, primaryPath, "commit", "--allow-empty", "-m", "initial")
				runTestGit(t, primaryPath, "branch", "other")

				wtPath := filepath.Join(t.TempDir(), "wt")
				runTestGit(t, primaryPath, "worktree", "add", "-b", "feat/worktree", wtPath, "HEAD")
				return wtPath
			},
			wantCurrentBranch: "feat/worktree",
			wantDetached:      false,
			wantBranches:      []string{"feat/worktree", "main", "other"},
			wantOutputSubstr:  "* feat/worktree",
		},
		{
			name: "primary repository when linked git worktree exists",
			setup: func(t *testing.T) string {
				primaryPath := t.TempDir()
				runTestGit(t, primaryPath, "init", "-b", "main")
				runTestGit(t, primaryPath, "commit", "--allow-empty", "-m", "initial")

				wtPath := filepath.Join(t.TempDir(), "wt")
				runTestGit(t, primaryPath, "worktree", "add", "-b", "feat/worktree", wtPath, "HEAD")
				return primaryPath
			},
			wantCurrentBranch: "main",
			wantDetached:      false,
			wantBranches:      []string{"feat/worktree", "main"},
			wantOutputSubstr:  "* main",
		},
		{
			name: "branch sharing name with tag",
			setup: func(t *testing.T) string {
				repoPath := t.TempDir()
				runTestGit(t, repoPath, "init", "-b", "v1.0.0")
				runTestGit(t, repoPath, "commit", "--allow-empty", "-m", "release")
				runTestGit(t, repoPath, "tag", "v1.0.0")
				return repoPath
			},
			wantCurrentBranch: "v1.0.0",
			wantDetached:      false,
			wantBranches:      []string{"v1.0.0"},
		},
	}

	p := NewPlugin()
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			targetPath := tt.setup(t)

			result, err := p.ExecuteProvider(context.Background(), "git", map[string]any{
				"operation": "branch",
				"path":      targetPath,
			})
			require.NoError(t, err)
			require.NotNil(t, result)

			data, ok := result.Data.(map[string]any)
			require.True(t, ok)
			assert.Equal(t, true, data["success"])
			assert.Equal(t, "", data["error"])
			assert.Equal(t, "branch", data["operation"])
			assert.Equal(t, targetPath, data["path"])
			assert.Equal(t, tt.wantCurrentBranch, data["currentBranch"])
			assert.Equal(t, tt.wantDetached, data["detached"])

			gotBranches, ok := data["branches"].([]string)
			require.True(t, ok, "branches must be a non-nil []string")
			assert.NotNil(t, gotBranches)
			assert.Equal(t, tt.wantBranches, gotBranches)

			if tt.wantOutputSubstr != "" {
				assert.Contains(t, data["output"], tt.wantOutputSubstr)
			}

			encoded, err := json.Marshal(result)
			require.NoError(t, err)
			if len(tt.wantBranches) == 0 {
				assert.Contains(t, string(encoded), `"branches":[]`)
			}
		})
	}
}

func TestExecuteProvider_Branch_CreatePreservesMutationOutput(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git command not available")
	}

	repoPath := t.TempDir()
	runTestGit(t, repoPath, "init", "-b", "main")
	runTestGit(t, repoPath, "commit", "--allow-empty", "-m", "initial")

	p := NewPlugin()
	createRes, err := p.ExecuteProvider(context.Background(), "git", map[string]any{
		"operation": "branch",
		"path":      repoPath,
		"branch":    "feat/created",
	})
	require.NoError(t, err)
	createData := createRes.Data.(map[string]any)
	assert.Equal(t, true, createData["success"])
	assert.Equal(t, "branch", createData["operation"])
	assert.Equal(t, repoPath, createData["path"])
	_, hasCurrent := createData["currentBranch"]
	assert.False(t, hasCurrent)
	_, hasDetached := createData["detached"]
	assert.False(t, hasDetached)
	_, hasBranches := createData["branches"]
	assert.False(t, hasBranches)

	listRes, err := p.ExecuteProvider(context.Background(), "git", map[string]any{
		"operation": "branch",
		"path":      repoPath,
	})
	require.NoError(t, err)
	listData := listRes.Data.(map[string]any)
	assert.Equal(t, "main", listData["currentBranch"])
	assert.Equal(t, false, listData["detached"])
	assert.Equal(t, []string{"feat/created", "main"}, listData["branches"])

	flagRes, err := p.ExecuteProvider(context.Background(), "git", map[string]any{
		"operation": "branch",
		"path":      repoPath,
		"branch":    "--show-current",
	})
	require.NoError(t, err)
	flagData := flagRes.Data.(map[string]any)
	assert.Equal(t, false, flagData["success"])
}

func TestExecuteProvider_Branch_NotGitRepository(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git command not available")
	}

	emptyDir := t.TempDir()
	p := NewPlugin()
	result, err := p.ExecuteProvider(context.Background(), "git", map[string]any{
		"operation": "branch",
		"path":      emptyDir,
	})
	require.NoError(t, err)
	data := result.Data.(map[string]any)
	assert.Equal(t, false, data["success"])
	assert.NotEmpty(t, data["error"])
	_, hasCurrent := data["currentBranch"]
	assert.False(t, hasCurrent)
}

func TestExecuteProvider_Branch_CanceledContext(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git command not available")
	}

	repoPath := t.TempDir()
	runTestGit(t, repoPath, "init", "-b", "main")

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, _, _, err := resolveBranchMetadata(ctx, repoPath)
	require.Error(t, err)
	assert.ErrorIs(t, err, context.Canceled)

	p := NewPlugin()
	_, execErr := p.ExecuteProvider(ctx, "git", map[string]any{
		"operation": "branch",
		"path":      repoPath,
	})
	require.Error(t, execErr)
}

func TestResolveBranchMetadata_NonHeadSymbolicRef(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git command not available")
	}

	repoPath := t.TempDir()
	runTestGit(t, repoPath, "init", "-b", "main")
	runTestGit(t, repoPath, "commit", "--allow-empty", "-m", "initial")
	runTestGit(t, repoPath, "update-ref", "refs/custom/preview", "HEAD")
	runTestGit(t, repoPath, "symbolic-ref", "HEAD", "refs/custom/preview")

	currentBranch, detached, branches, err := resolveBranchMetadata(context.Background(), repoPath)
	require.NoError(t, err)
	assert.Equal(t, "", currentBranch)
	assert.True(t, detached)
	assert.Equal(t, []string{"main"}, branches)
}

func TestExecuteProvider_Tag_NonexistentPath(t *testing.T) {
	p := NewPlugin()
	_, err := p.ExecuteProvider(context.Background(), "git", map[string]any{
		"operation": "tag",
		"path":      "/nonexistent",
	})
	require.Error(t, err)
}

func TestExecuteProvider_Remote(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git command not available")
	}

	repoPath := t.TempDir()
	runGit := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...) //nolint:gosec // G204: test-only, args are literals
		cmd.Dir = repoPath
		require.NoError(t, cmd.Run(), "git %v", args)
	}
	runGit("init")
	runGit("remote", "add", "origin", "https://github.com/acme/widgets.git")

	p := NewPlugin()
	result, err := p.ExecuteProvider(context.Background(), "git", map[string]any{
		"operation": "remote",
		"path":      repoPath,
		"remote":    "origin",
	})
	require.NoError(t, err)
	require.NotNil(t, result)

	data := result.Data.(map[string]any)
	assert.Equal(t, "remote", data["operation"])
	assert.Equal(t, repoPath, data["path"])
	assert.Equal(t, "https://github.com/acme/widgets.git", data["url"])
	assert.Equal(t, "github.com", data["host"])
	assert.Equal(t, "acme", data["org"])
	assert.Equal(t, "widgets", data["repo"])
	assert.Equal(t, "https://github.com/acme/widgets", data["httpUrl"])
	assert.Equal(t, "git@github.com:acme/widgets.git", data["sshUrl"])
}

func TestExecuteProvider_Remote_DefaultRemote(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git command not available")
	}

	repoPath := t.TempDir()
	runGit := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...) //nolint:gosec // G204: test-only, args are literals
		cmd.Dir = repoPath
		require.NoError(t, cmd.Run(), "git %v", args)
	}
	runGit("init")
	runGit("remote", "add", "origin", "git@github.com:acme/widgets.git")

	p := NewPlugin()
	result, err := p.ExecuteProvider(context.Background(), "git", map[string]any{
		"operation": "remote",
		"path":      repoPath,
	})
	require.NoError(t, err)

	data := result.Data.(map[string]any)
	assert.Equal(t, "github.com", data["host"])
	assert.Equal(t, "acme", data["org"])
	assert.Equal(t, "widgets", data["repo"])
}

func TestExecuteProvider_Remote_UnknownRemote(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git command not available")
	}

	repoPath := t.TempDir()
	cmd := exec.Command("git", "init")
	cmd.Dir = repoPath
	require.NoError(t, cmd.Run())

	p := NewPlugin()
	_, err := p.ExecuteProvider(context.Background(), "git", map[string]any{
		"operation": "remote",
		"path":      repoPath,
		"remote":    "nonexistent",
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "remote")
}

// =============================================================================
// Dry-run tests
// =============================================================================

func TestDryRun_Clone(t *testing.T) {
	p := NewPlugin()
	ctx := sdkprovider.WithDryRun(context.Background(), true)

	result, err := p.ExecuteProvider(ctx, "git", map[string]any{
		"operation":  "clone",
		"repository": "https://github.com/user/repo.git",
		"path":       "/tmp/test-repo",
		"branch":     "main",
	})
	require.NoError(t, err)
	data := result.Data.(map[string]any)
	assert.True(t, data["success"].(bool))
	assert.Equal(t, "clone", data["operation"])
	assert.True(t, data["_dryRun"].(bool))
	assert.Contains(t, data["_message"].(string), "Would execute git clone")
	assert.Contains(t, data["_message"].(string), "https://github.com/user/repo.git")
	assert.Contains(t, data["_message"].(string), "main")
}

func TestDryRun_Status(t *testing.T) {
	p := NewPlugin()
	ctx := sdkprovider.WithDryRun(context.Background(), true)

	result, err := p.ExecuteProvider(ctx, "git", map[string]any{
		"operation": "status",
		"path":      "/tmp/test-repo",
	})
	require.NoError(t, err)
	data := result.Data.(map[string]any)
	assert.True(t, data["success"].(bool))
	assert.Equal(t, "status", data["operation"])
	assert.True(t, data["_dryRun"].(bool))
}

func TestDryRun_Push(t *testing.T) {
	p := NewPlugin()
	ctx := sdkprovider.WithDryRun(context.Background(), true)

	result, err := p.ExecuteProvider(ctx, "git", map[string]any{
		"operation": "push",
		"path":      "/tmp/repo",
		"branch":    "main",
	})
	require.NoError(t, err)
	data := result.Data.(map[string]any)
	assert.True(t, data["_dryRun"].(bool))
	assert.Contains(t, data["_message"].(string), "push")
	assert.Contains(t, data["_message"].(string), "main")
}

func TestDryRun_Log(t *testing.T) {
	p := NewPlugin()
	ctx := sdkprovider.WithDryRun(context.Background(), true)

	result, err := p.ExecuteProvider(ctx, "git", map[string]any{
		"operation": "log",
		"path":      "/tmp/repo",
	})
	require.NoError(t, err)
	data := result.Data.(map[string]any)
	assert.True(t, data["_dryRun"].(bool))
	assert.Contains(t, data["_message"].(string), "log")
}

func TestDryRun_Tag(t *testing.T) {
	p := NewPlugin()
	ctx := sdkprovider.WithDryRun(context.Background(), true)

	result, err := p.ExecuteProvider(ctx, "git", map[string]any{
		"operation": "tag",
		"path":      "/tmp/repo",
	})
	require.NoError(t, err)
	data := result.Data.(map[string]any)
	assert.True(t, data["_dryRun"].(bool))
	assert.Contains(t, data["_message"].(string), "tag")
}

func TestDryRun_Commit(t *testing.T) {
	p := NewPlugin()
	ctx := sdkprovider.WithDryRun(context.Background(), true)

	result, err := p.ExecuteProvider(ctx, "git", map[string]any{
		"operation": "commit",
		"path":      "/tmp/repo",
		"message":   "fix: dry run test",
	})
	require.NoError(t, err)
	data := result.Data.(map[string]any)
	assert.True(t, data["_dryRun"].(bool))
}

func TestDryRun_Checkout(t *testing.T) {
	p := NewPlugin()
	ctx := sdkprovider.WithDryRun(context.Background(), true)

	result, err := p.ExecuteProvider(ctx, "git", map[string]any{
		"operation": "checkout",
		"path":      "/tmp/repo",
		"branch":    "feature",
	})
	require.NoError(t, err)
	data := result.Data.(map[string]any)
	assert.True(t, data["_dryRun"].(bool))
	assert.Contains(t, data["_message"].(string), "checkout")
}

func TestDryRun_Pull(t *testing.T) {
	p := NewPlugin()
	ctx := sdkprovider.WithDryRun(context.Background(), true)

	result, err := p.ExecuteProvider(ctx, "git", map[string]any{
		"operation": "pull",
		"path":      "/tmp/repo",
		"branch":    "develop",
	})
	require.NoError(t, err)
	data := result.Data.(map[string]any)
	assert.True(t, data["_dryRun"].(bool))
	assert.Contains(t, data["_message"].(string), "pull")
}

func TestDryRun_Add(t *testing.T) {
	p := NewPlugin()
	ctx := sdkprovider.WithDryRun(context.Background(), true)

	result, err := p.ExecuteProvider(ctx, "git", map[string]any{
		"operation": "add",
		"path":      "/tmp/repo",
	})
	require.NoError(t, err)
	data := result.Data.(map[string]any)
	assert.True(t, data["_dryRun"].(bool))
	assert.Contains(t, data["_message"].(string), "add")
}

func TestDryRun_Branch(t *testing.T) {
	p := NewPlugin()
	ctx := sdkprovider.WithDryRun(context.Background(), true)

	result, err := p.ExecuteProvider(ctx, "git", map[string]any{
		"operation": "branch",
		"path":      "/tmp/repo",
		"branch":    "new-branch",
	})
	require.NoError(t, err)
	data := result.Data.(map[string]any)
	assert.True(t, data["_dryRun"].(bool))
	assert.Contains(t, data["_message"].(string), "branch")
	assert.Contains(t, data["_message"].(string), "new-branch")
	_, hasCurrent := data["currentBranch"]
	assert.False(t, hasCurrent)
}

func TestDryRun_Branch_ReadOnly(t *testing.T) {
	p := NewPlugin()
	ctx := sdkprovider.WithDryRun(context.Background(), true)

	result, err := p.ExecuteProvider(ctx, "git", map[string]any{
		"operation": "branch",
		"path":      "/tmp/repo",
	})
	require.NoError(t, err)
	data := result.Data.(map[string]any)
	assert.True(t, data["_dryRun"].(bool))
	assert.Equal(t, "", data["currentBranch"])
	assert.Equal(t, false, data["detached"])
	assert.Equal(t, []string{}, data["branches"])
}

func TestDryRun_CloneDepthFloat(t *testing.T) {
	p := NewPlugin()
	ctx := sdkprovider.WithDryRun(context.Background(), true)

	result, err := p.ExecuteProvider(ctx, "git", map[string]any{
		"operation":  "clone",
		"repository": "https://example.com/nonexistent.git",
		"path":       "/tmp/clone-target",
		"depth":      float64(1),
		"branch":     "main",
	})
	require.NoError(t, err)
	data := result.Data.(map[string]any)
	assert.True(t, data["_dryRun"].(bool))
	assert.Contains(t, data["_message"].(string), "clone")
}

// =============================================================================
// DescribeWhatIf tests
// =============================================================================

func TestDescribeWhatIf_Clone(t *testing.T) {
	p := NewPlugin()
	msg, err := p.DescribeWhatIf(context.Background(), "git", map[string]any{
		"operation":  "clone",
		"repository": "https://github.com/user/repo.git",
		"path":       "/tmp/repo",
		"branch":     "main",
	})
	require.NoError(t, err)
	assert.Contains(t, msg, "clone")
	assert.Contains(t, msg, "https://github.com/user/repo.git")
	assert.Contains(t, msg, "/tmp/repo")
	assert.Contains(t, msg, "main")
}

func TestDescribeWhatIf_CloneMinimal(t *testing.T) {
	p := NewPlugin()
	msg, err := p.DescribeWhatIf(context.Background(), "git", map[string]any{
		"operation":  "clone",
		"repository": "https://github.com/user/repo.git",
	})
	require.NoError(t, err)
	assert.Contains(t, msg, "clone")
}

func TestDescribeWhatIf_Commit(t *testing.T) {
	p := NewPlugin()
	msg, err := p.DescribeWhatIf(context.Background(), "git", map[string]any{
		"operation": "commit",
		"path":      "/tmp/repo",
		"message":   "fix: something",
	})
	require.NoError(t, err)
	assert.Contains(t, msg, "commit")
	assert.Contains(t, msg, "fix: something")
}

func TestDescribeWhatIf_Push(t *testing.T) {
	p := NewPlugin()
	msg, err := p.DescribeWhatIf(context.Background(), "git", map[string]any{
		"operation": "push",
		"path":      "/tmp/repo",
		"branch":    "develop",
	})
	require.NoError(t, err)
	assert.Contains(t, msg, "push")
	assert.Contains(t, msg, "develop")
}

func TestDescribeWhatIf_Checkout(t *testing.T) {
	p := NewPlugin()
	msg, err := p.DescribeWhatIf(context.Background(), "git", map[string]any{
		"operation": "checkout",
		"path":      "/tmp/repo",
		"branch":    "feature",
	})
	require.NoError(t, err)
	assert.Contains(t, msg, "checkout")
	assert.Contains(t, msg, "feature")
}

func TestDescribeWhatIf_Branch(t *testing.T) {
	p := NewPlugin()

	listMsg, err := p.DescribeWhatIf(context.Background(), "git", map[string]any{
		"operation": "branch",
		"path":      "/tmp/repo",
	})
	require.NoError(t, err)
	assert.Contains(t, listMsg, "list branches")
	assert.Contains(t, listMsg, "/tmp/repo")

	listNoPathMsg, err := p.DescribeWhatIf(context.Background(), "git", map[string]any{
		"operation": "branch",
	})
	require.NoError(t, err)
	assert.Contains(t, listNoPathMsg, "list branches")

	createMsg, err := p.DescribeWhatIf(context.Background(), "git", map[string]any{
		"operation": "branch",
		"path":      "/tmp/repo",
		"branch":    "feat/demo",
	})
	require.NoError(t, err)
	assert.Contains(t, createMsg, "create branch")
	assert.Contains(t, createMsg, "feat/demo")
	assert.Contains(t, createMsg, "/tmp/repo")

	createNoPathMsg, err := p.DescribeWhatIf(context.Background(), "git", map[string]any{
		"operation": "branch",
		"branch":    "feat/demo",
	})
	require.NoError(t, err)
	assert.Contains(t, createNoPathMsg, "create branch")
	assert.Contains(t, createNoPathMsg, "feat/demo")
}

func TestDescribeWhatIf_Tag(t *testing.T) {
	p := NewPlugin()
	msg, err := p.DescribeWhatIf(context.Background(), "git", map[string]any{
		"operation": "tag",
		"path":      "/tmp/repo",
		"tag":       "v1.0.0",
	})
	require.NoError(t, err)
	assert.Contains(t, msg, "tag")
	assert.Contains(t, msg, "v1.0.0")
}

func TestDescribeWhatIf_Remote(t *testing.T) {
	p := NewPlugin()
	msg, err := p.DescribeWhatIf(context.Background(), "git", map[string]any{
		"operation": "remote",
		"path":      "/tmp/repo",
		"remote":    "upstream",
	})
	require.NoError(t, err)
	assert.Contains(t, msg, "remote")
	assert.Contains(t, msg, "upstream")
	assert.Contains(t, msg, "/tmp/repo")
}

func TestDescribeWhatIf_RemoteDefaults(t *testing.T) {
	p := NewPlugin()
	msg, err := p.DescribeWhatIf(context.Background(), "git", map[string]any{
		"operation": "remote",
	})
	require.NoError(t, err)
	assert.Contains(t, msg, "remote")
	assert.Contains(t, msg, "origin")
}

func TestDescribeWhatIf_DefaultOperation(t *testing.T) {
	p := NewPlugin()
	msg, err := p.DescribeWhatIf(context.Background(), "git", map[string]any{
		"operation": "status",
		"path":      "/tmp/repo",
	})
	require.NoError(t, err)
	assert.Contains(t, msg, "status")
	assert.Contains(t, msg, "/tmp/repo")
}

func TestDescribeWhatIf_DefaultOperationNoPath(t *testing.T) {
	p := NewPlugin()
	msg, err := p.DescribeWhatIf(context.Background(), "git", map[string]any{
		"operation": "log",
	})
	require.NoError(t, err)
	assert.Contains(t, msg, "log")
}

func TestDescribeWhatIf_UnknownProvider(t *testing.T) {
	p := NewPlugin()
	_, err := p.DescribeWhatIf(context.Background(), "nope", nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "unknown provider")
}

// =============================================================================
// Netrc credential tests
// =============================================================================

func TestCreateNetrcCredentials(t *testing.T) {
	tests := []struct {
		name       string
		repoURL    string
		username   string
		password   string
		wantNilEnv bool
	}{
		{
			name:     "https URL creates netrc",
			repoURL:  "https://github.com/user/repo.git",
			username: "testuser",
			password: "testpass",
		},
		{
			name:     "http URL creates netrc",
			repoURL:  "http://internal.example.com/repo.git",
			username: "testuser",
			password: "testpass",
		},
		{
			name:       "ssh URL returns nil env",
			repoURL:    "git@github.com:user/repo.git",
			username:   "testuser",
			password:   "testpass",
			wantNilEnv: true,
		},
		{ //nolint:gosec // G101: test-only credentials, not real secrets
			name:     "special characters in credentials written as-is",
			repoURL:  "https://github.com/org/repo",
			username: "user@corp",
			password: "p@ss:w/rd%100",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			env, cleanup, err := createNetrcCredentials(tt.repoURL, tt.username, tt.password)
			require.NoError(t, err)
			require.NotNil(t, cleanup)
			defer cleanup()

			if tt.wantNilEnv {
				assert.Nil(t, env)
				return
			}

			require.NotNil(t, env)
			var homeDir string
			for _, e := range env {
				if len(e) > 5 && e[:5] == "HOME=" {
					homeDir = e[5:]
					break
				}
			}
			require.NotEmpty(t, homeDir, "HOME must be set in credential env")

			netrcPath := filepath.Join(homeDir, ".netrc")
			info, err := os.Stat(netrcPath)
			require.NoError(t, err, ".netrc file should exist")
			assert.Equal(t, os.FileMode(0o600), info.Mode().Perm(), ".netrc should be mode 0600")

			content, err := os.ReadFile(netrcPath) //nolint:gosec // G304: path is constructed from test temp dir
			require.NoError(t, err)
			contentStr := string(content)
			assert.Contains(t, contentStr, "login "+tt.username)
			assert.Contains(t, contentStr, "password "+tt.password)
		})
	}
}

func TestCreateNetrcCredentials_EmptyHostname(t *testing.T) {
	_, _, err := createNetrcCredentials("https:///path", "user", "pass")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "no hostname")
}

func TestCreateNetrcCredentials_WhitespaceRejection(t *testing.T) {
	tests := []struct {
		name     string
		username string
		password string
		wantErr  string
	}{
		{"space in username", "user name", "pass", "username contains whitespace"},
		{"tab in password", "user", "pass\tword", "password contains whitespace"},
		{"newline in password", "user", "pass\nword", "password contains whitespace"},
		{"newline in username", "user\nname", "pass", "username contains whitespace"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, _, err := createNetrcCredentials("https://github.com/org/repo.git", tt.username, tt.password)
			require.Error(t, err)
			assert.Contains(t, err.Error(), tt.wantErr)
		})
	}
}

// =============================================================================
// Environment override tests
// =============================================================================

func TestApplyEnvOverrides(t *testing.T) {
	t.Run("basic override", func(t *testing.T) {
		base := []string{"HOME=/original", "PATH=/usr/bin"}
		overrides := map[string]string{"HOME": "/tmp/fake"}
		result := applyEnvOverrides(base, overrides)
		assert.Contains(t, result, "HOME=/tmp/fake")
		assert.Contains(t, result, "PATH=/usr/bin")
		assert.NotContains(t, result, "HOME=/original")
	})

	t.Run("case insensitive", func(t *testing.T) {
		base := []string{"home=/original"}
		overrides := map[string]string{"HOME": "/tmp/fake"}
		result := applyEnvOverrides(base, overrides)
		assert.Contains(t, result, "HOME=/tmp/fake")
		assert.NotContains(t, result, "home=/original")
	})

	t.Run("no override", func(t *testing.T) {
		base := []string{"FOO=bar", "BAZ=qux"}
		overrides := map[string]string{"OTHER": "value"}
		result := applyEnvOverrides(base, overrides)
		assert.Contains(t, result, "FOO=bar")
		assert.Contains(t, result, "BAZ=qux")
		assert.Contains(t, result, "OTHER=value")
	})

	t.Run("adds new keys", func(t *testing.T) {
		base := []string{"FOO=bar"}
		overrides := map[string]string{"NEW": "val"}
		result := applyEnvOverrides(base, overrides)
		assert.Contains(t, result, "FOO=bar")
		assert.Contains(t, result, "NEW=val")
	})

	t.Run("empty inputs", func(t *testing.T) {
		result := applyEnvOverrides(nil, nil)
		assert.Empty(t, result)
	})

	t.Run("base only", func(t *testing.T) {
		result := applyEnvOverrides([]string{"FOO=bar", "BAZ=qux"}, nil)
		assert.Equal(t, []string{"FOO=bar", "BAZ=qux"}, result)
	})

	t.Run("overrides only", func(t *testing.T) {
		result := applyEnvOverrides(nil, map[string]string{"NEW": "val"})
		assert.Contains(t, result, "NEW=val")
	})

	t.Run("base entry without equals", func(t *testing.T) {
		result := applyEnvOverrides([]string{"ORPHAN"}, map[string]string{"orphan": "replaced"})
		assert.Contains(t, result, "orphan=replaced")
		assert.NotContains(t, result, "ORPHAN")
	})
}

// =============================================================================
// Plugin interface tests
// =============================================================================

func TestConfigureProvider(t *testing.T) {
	p := NewPlugin()
	err := p.ConfigureProvider(context.Background(), "git", sdkplugin.ProviderConfig{})
	assert.NoError(t, err)
}

func TestConfigureProvider_Unknown(t *testing.T) {
	p := NewPlugin()
	err := p.ConfigureProvider(context.Background(), "nope", sdkplugin.ProviderConfig{})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "unknown provider")
}

func TestExtractDependencies(t *testing.T) {
	p := NewPlugin()
	deps, err := p.ExtractDependencies(context.Background(), "git", nil)
	assert.NoError(t, err)
	assert.Nil(t, deps)
}

func TestStopProvider(t *testing.T) {
	p := NewPlugin()
	err := p.StopProvider(context.Background(), "git")
	assert.NoError(t, err)
}

func TestExecuteProviderStream(t *testing.T) {
	p := NewPlugin()
	err := p.ExecuteProviderStream(context.Background(), "git", nil, nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "streaming")
}
