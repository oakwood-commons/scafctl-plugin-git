package git

import (
	"context"
	"os/exec"
	"testing"

	sdkprovider "github.com/oakwood-commons/scafctl-plugin-sdk/provider"
)

func BenchmarkExecuteProvider_DryRun(b *testing.B) {
	p := NewPlugin()
	ctx := sdkprovider.WithDryRun(context.Background(), true)
	inputs := map[string]any{
		"operation":  "clone",
		"repository": "https://github.com/example/repo.git",
		"path":       "/tmp/repo",
	}

	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		_, _ = p.ExecuteProvider(ctx, "git", inputs)
	}
}

func BenchmarkDescribeWhatIf(b *testing.B) {
	p := NewPlugin()
	inputs := map[string]any{
		"operation":  "clone",
		"repository": "https://github.com/user/repo.git",
		"path":       "/tmp/repo",
		"branch":     "main",
	}

	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		_, _ = p.DescribeWhatIf(context.Background(), "git", inputs)
	}
}

func BenchmarkParseRemoteURL(b *testing.B) {
	urls := []string{
		"https://github.com/acme/widgets.git",
		"git@github.com:acme/widgets.git",
		"ssh://git@github.com:22/group/subgroup/widgets.git",
	}

	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		for _, u := range urls {
			_, _ = ParseRemoteURL(u)
		}
	}
}

func BenchmarkExecuteProvider_Branch(b *testing.B) {
	if _, err := exec.LookPath("git"); err != nil {
		b.Skip("git command not available")
	}

	repoPath := b.TempDir()
	for _, args := range [][]string{
		{"init", "-b", "main"},
		{"-c", "user.name=Bench", "-c", "user.email=bench@example.com", "-c", "commit.gpgsign=false", "commit", "--allow-empty", "-m", "init"},
		{"checkout", "-b", "feat/bench"},
	} {
		cmd := exec.Command("git", args...) //nolint:gosec // G204: benchmark setup with static args
		cmd.Dir = repoPath
		if out, err := cmd.CombinedOutput(); err != nil {
			b.Fatalf("git %v failed: %s", args, string(out))
		}
	}

	p := NewPlugin()
	inputs := map[string]any{
		"operation": "branch",
		"path":      repoPath,
	}

	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		_, _ = p.ExecuteProvider(context.Background(), "git", inputs)
	}
}

func BenchmarkParseNumstatZ(b *testing.B) {
	sample := []byte("12\t4\tinternal/git/provider.go\x00-\t-\tassets/logo.png\x003\t1\t\x00old dir/a.go\x00new dir/a.go\x00")

	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		_, _, _, _, _ = parseNumstatZ(sample)
	}
}

func BenchmarkExecuteProvider_DiffAndMergeCheck(b *testing.B) {
	if _, err := exec.LookPath("git"); err != nil {
		b.Skip("git command not available")
	}

	repoPath := b.TempDir()
	for _, args := range [][]string{
		{"init", "-b", "main"},
		{"-c", "user.name=Bench", "-c", "user.email=bench@example.com", "-c", "commit.gpgsign=false", "commit", "--allow-empty", "-m", "base"},
		{"checkout", "-b", "feat/bench"},
		{"-c", "user.name=Bench", "-c", "user.email=bench@example.com", "-c", "commit.gpgsign=false", "commit", "--allow-empty", "-m", "head"},
	} {
		cmd := exec.Command("git", args...) //nolint:gosec // G204: benchmark setup with static args
		cmd.Dir = repoPath
		if out, err := cmd.CombinedOutput(); err != nil {
			b.Fatalf("git %v failed: %s", args, string(out))
		}
	}

	p := NewPlugin()

	b.Run("diff", func(b *testing.B) {
		inputs := map[string]any{
			"operation": "diff",
			"path":      repoPath,
			"base":      "main",
			"head":      "feat/bench",
		}
		b.ReportAllocs()
		b.ResetTimer()
		for b.Loop() {
			_, _ = p.ExecuteProvider(context.Background(), "git", inputs)
		}
	})

	b.Run("merge-check", func(b *testing.B) {
		inputs := map[string]any{
			"operation": "merge-check",
			"path":      repoPath,
			"base":      "main",
			"head":      "feat/bench",
		}
		b.ReportAllocs()
		b.ResetTimer()
		for b.Loop() {
			_, _ = p.ExecuteProvider(context.Background(), "git", inputs)
		}
	})
}
