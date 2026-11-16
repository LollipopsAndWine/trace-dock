package app

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/trace-dock/trace-dock/internal/core"
	"github.com/trace-dock/trace-dock/internal/ui"
)

func TestCollectionProgressRetainsCompletedTrajectoryBetweenRefreshes(t *testing.T) {
	var progress collectionProgress
	progress.update("trajectory", "检查会话轨迹 session-one", 0, 1)
	progress.update("trajectory", "会话轨迹检查完成 session-one", 1, 1)
	// All of these events can arrive before the next 120 ms display refresh.
	progress.update("workspace:one", "扫描 workspace 文件：project-one", 1375, 0)
	progress.update("workspace:two", "扫描 workspace 文件：project-two", 24, 0)
	var output strings.Builder
	u := &ui.UI{Out: &output, TTY: true}
	u.ProgressLines(progress.lines(0))
	text := output.String()
	for _, want := range []string{"✓ 会话轨迹检查完成 session-one 1/1", "project-two · 已检查 24 项"} {
		if !strings.Contains(text, want) {
			t.Errorf("refresh lost an independent stage update %q: %s", want, text)
		}
	}
	if strings.Contains(text, "0/1") || strings.Contains(text, "准备本机扫描") {
		t.Fatalf("stale progress survived a stage transition: %s", text)
	}
}

func TestCollectionProgressShowsAllCompletedTrajectoryIDs(t *testing.T) {
	var progress collectionProgress
	progress.update("trajectory", "检查会话轨迹 first-session", 0, 2)
	progress.update("trajectory", "扫描轨迹 first-session", 1, 2)
	progress.update("trajectory", "检查会话轨迹 second-session", 1, 2)
	progress.update("trajectory", "扫描轨迹 second-session", 2, 2)
	progress.update("trajectory", "会话轨迹检查完成 second-session", 2, 2)

	line := progress.lines(0)["trajectory"]
	for _, want := range []string{"2/2", "first-session", "second-session"} {
		if !strings.Contains(line, want) {
			t.Fatalf("trajectory completion lost %q: %s", want, line)
		}
	}
}

func TestCollectionProgressKeepsConcurrentCountersTogether(t *testing.T) {
	var progress collectionProgress
	var workers sync.WaitGroup
	for _, name := range []string{"workspace:one", "workspace:two"} {
		workers.Add(1)
		go func(key string) {
			defer workers.Done()
			for i := 1; i <= 200; i++ {
				progress.update(key, key, i, i)
			}
		}(name)
	}
	for i := 0; i < 200; i++ {
		for _, line := range progress.lines(i) {
			if strings.Contains(line, "workspace 完成") {
				continue
			}
			if !strings.Contains(line, "/") || strings.Contains(line, "准备本机扫描") {
				continue
			}
			fields := strings.Fields(line)
			counter := strings.Split(fields[len(fields)-1], "/")
			if len(counter) != 2 || counter[0] != counter[1] {
				t.Errorf("stage label and counters were read from different events: %s", line)
			}
		}
	}
	workers.Wait()
	if len(progress.lines(0)) > 3 {
		t.Fatal("progress view expanded with one line per workspace")
	}
}

func TestCollectionProgressBoundsManyWorkspaceAndTrajectoryRows(t *testing.T) {
	var progress collectionProgress
	for i := 1; i <= 100; i++ {
		id := fmt.Sprintf("session-%03d", i)
		progress.update("trajectory", "扫描轨迹 "+id, i, 100)
		progress.update(fmt.Sprintf("workspace:%03d", i), fmt.Sprintf("扫描 workspace 文件：project-%03d", i), i*10, 0)
		progress.update(fmt.Sprintf("workspace:%03d", i), fmt.Sprintf("完成 workspace：project-%03d", i), i, 100)
	}
	progress.update("trajectory", "会话轨迹检查完成 session-100", 100, 100)
	lines := progress.lines(0)
	if len(lines) > 3 {
		t.Fatalf("progress rows grew with selection size: %d", len(lines))
	}
	line := lines["trajectory"]
	if !strings.Contains(line, "已完成 100 个") || !strings.Contains(line, "session-100") {
		t.Fatalf("large trajectory summary is not bounded: %s", line)
	}
	if !strings.Contains(lines["workspace-summary"], "100/100") {
		t.Fatalf("workspace completion summary missing: %s", lines["workspace-summary"])
	}
}

func TestWorkspaceCompletionCounterIgnoresCurrentFileBytes(t *testing.T) {
	var progress collectionProgress
	progress.update("workspace:one", "扫描 workspace 文件：.venv/site-packages/large-file", 1496, 27835968)
	progress.update("workspace:one", "完成 workspace：project-one", 1, 3)
	progress.update("workspace:two", "扫描 workspace 文件：.venv/site-packages/another-file", 2048, 8912345)

	lines := progress.lines(0)
	if got := lines["workspace-summary"]; !strings.Contains(got, "1/3") {
		t.Fatalf("workspace summary used a file byte count as workspace total: %s", got)
	}
	if strings.Contains(lines["workspace-summary"], "27835968") || strings.Contains(lines["workspace-summary"], "8912345") {
		t.Fatalf("file byte totals leaked into workspace completion summary: %s", lines["workspace-summary"])
	}
}

func TestCollectionFlushesCompletionForFastAndExcludedTrajectories(t *testing.T) {
	home := t.TempDir()
	var sessions []*core.Session
	for i, content := range []string{
		`{"type":"user","message":{"content":"hello"}}` + "\n",
		`{"type":"user","message":{"content":"password=UnitPass9#fixture"}}` + "\n",
	} {
		id := []string{"safe", "excluded"}[i]
		source := filepath.Join(home, id+".jsonl")
		if err := core.WritePrivate(source, []byte(content)); err != nil {
			t.Fatal(err)
		}
		sessions = append(sessions, &core.Session{Tool: "claude", ID: id, Source: source, Workspace: core.Classify("", home), Eligibility: "archive_only"})
	}
	scanner, err := core.NewScanner()
	if err != nil {
		t.Fatal(err)
	}
	collector := &core.Collector{Dir: t.TempDir(), Home: home, Scanner: scanner}
	m := &core.Manifest{JobID: "job", Tool: "claude", ConsentedAt: time.Now(), Rules: core.Rules{LargeFileThreshold: 1 << 30}, Sessions: sessions, Selection: []string{"safe", "excluded"}}
	var output strings.Builder
	u := &ui.UI{Out: &output}
	if err := collectWithProgress(context.Background(), u, collector, m); err != nil {
		t.Fatal(err)
	}
	if m.Summary.Excluded != 1 {
		t.Fatalf("fixture did not exercise trajectory exclusion: %+v", m.Summary)
	}
	if !strings.Contains(output.String(), "✓ 会话轨迹检查完成 2/2 · 会话：safe、excluded") {
		t.Fatalf("completion before the next refresh was not flushed: %s", output.String())
	}
}
