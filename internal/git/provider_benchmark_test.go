package git

import (
	"context"
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
