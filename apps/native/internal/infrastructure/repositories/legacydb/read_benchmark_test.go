//go:build compatibility

package legacydb

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"liberty.local/liberty/internal/domain"
)

func TestCompareReleaseReadLatency(t *testing.T) {
	directory := os.Getenv("LIBERTY_NATIVE_FIXTURE_DIR")
	if !filepath.IsAbs(directory) {
		t.Skip("pnpm native:bench:reads generates the comparison fixture")
	}
	ctx := context.Background()
	reader, err := Open(ctx, filepath.Join(directory, "reads.sqlite3"))
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close()
	for _, operation := range []string{"queue", "transcript"} {
		var samples []int64
		for iteration := range 22 {
			start := time.Now()
			switch operation {
			case "queue":
				jobs, err := reader.ListJobs(ctx)
				if err != nil || len(jobs) != 1000 {
					t.Fatal("queue projection", err)
				}
				var files int
				for _, job := range jobs {
					files += len(job.SourceFiles)
				}
				if files != 1000 {
					t.Fatal("missing source files")
				}
			case "transcript":
				result, err := reader.ReadResult(ctx, "job-1700000000000-0")
				if err != nil || len(result.Transcript) != 10000 || result.Transcript[9999].Text != "合成会议记录 9999" {
					t.Fatal("transcript projection", err)
				}
			}
			if iteration >= 2 {
				samples = append(samples, time.Since(start).Microseconds())
			}
		}
		slices.Sort(samples)
		t.Logf("go %s: p50=%dus p95=%dus n=20", operation, samples[9], samples[18])
	}
}

func BenchmarkReadProjection(benchmark *testing.B) {
	directory := os.Getenv("LIBERTY_NATIVE_FIXTURE_DIR")
	if !filepath.IsAbs(directory) {
		benchmark.Skip("requires the Rust-generated read fixture")
	}
	ctx := context.Background()
	reader, err := Open(ctx, filepath.Join(directory, "reads.sqlite3"))
	if err != nil {
		benchmark.Fatal(err)
	}
	benchmark.Cleanup(func() { _ = reader.Close() })
	for _, operation := range []string{"queue", "queue-page", "transcript", "revision"} {
		benchmark.Run(operation, func(benchmark *testing.B) {
			benchmark.ReportAllocs()
			for range benchmark.N {
				switch operation {
				case "queue-page":
					page, err := reader.QueryJobs(ctx, domain.QueueQuery{Source: "local", Page: 1})
					if err != nil || page.Total != 1000 || len(page.Items) != domain.QueuePageSize {
						benchmark.Fatal("paged queue", err)
					}
				case "revision":
					if revision, err := reader.ReadRevision(ctx); err != nil || revision == 0 {
						benchmark.Fatal("revision", err)
					}
				case "queue":
					jobs, err := reader.ListJobs(ctx)
					if err != nil || len(jobs) != 1000 {
						benchmark.Fatal("queue projection", err)
					}
				case "transcript":
					result, err := reader.ReadResult(ctx, "job-1700000000000-0")
					if err != nil || len(result.Transcript) != 10000 || result.Transcript[9999].Text != "合成会议记录 9999" {
						benchmark.Fatal("transcript projection", err)
					}
				}
			}
		})
	}
}
