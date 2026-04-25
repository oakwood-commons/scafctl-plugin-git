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
