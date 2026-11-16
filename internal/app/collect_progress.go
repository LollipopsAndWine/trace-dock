package app

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/trace-dock/trace-dock/internal/core"
	"github.com/trace-dock/trace-dock/internal/ui"
)

type collectionStage struct {
	name           string
	current, total int
}

type collectionProgress struct {
	mu               sync.Mutex
	stages           map[string]collectionStage
	trajectoryIDs    []string
	trajectoryIDSeen map[string]bool
	workspaceLatest  collectionStage
	workspaceDone    int
	workspaceTotal   int
}

func (p *collectionProgress) update(key, name string, current, total int) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.stages == nil {
		p.stages = map[string]collectionStage{}
	}
	if p.trajectoryIDSeen == nil {
		p.trajectoryIDSeen = map[string]bool{}
	}
	if strings.HasPrefix(key, "workspace:") {
		// File scan events use total=bytes in the current file. Only the
		// workspace completion event carries the workspace count.
		if strings.HasPrefix(name, "完成 workspace：") {
			p.workspaceTotal = max(p.workspaceTotal, total)
			p.workspaceDone = max(p.workspaceDone, current)
		}
		if strings.HasPrefix(name, "扫描 workspace") || strings.HasPrefix(name, "完成 workspace：") {
			p.workspaceLatest = collectionStage{name: name, current: current, total: total}
		}
	}
	if key == "trajectory" && current > 0 {
		for _, prefix := range []string{"检查会话轨迹 ", "扫描轨迹 "} {
			if strings.HasPrefix(name, prefix) {
				id := strings.TrimSpace(strings.TrimPrefix(name, prefix))
				if id != "" && !p.trajectoryIDSeen[id] {
					p.trajectoryIDSeen[id] = true
					p.trajectoryIDs = append(p.trajectoryIDs, id)
				}
				break
			}
		}
	}
	p.stages[key] = collectionStage{name: name, current: current, total: total}
}

func (p *collectionProgress) lines(frame int) map[string]string {
	p.mu.Lock()
	defer p.mu.Unlock()
	lines := map[string]string{}
	frames := []string{"|", "/", "-", "\\"}
	for key, stage := range p.stages {
		if key != "trajectory" && !strings.HasPrefix(key, "workspace:") {
			lines[key] = frames[frame%len(frames)] + " " + stage.name
		}
	}
	if stage, ok := p.stages["trajectory"]; ok {
		status := stage.name
		if strings.HasPrefix(stage.name, "会话轨迹检查完成") && len(p.trajectoryIDs) > 1 {
			status = trajectoryCompletionStatus(stage.current, stage.total, p.trajectoryIDs)
		}
		if stage.total > 0 {
			if strings.HasPrefix(stage.name, "会话轨迹检查完成") && len(p.trajectoryIDs) > 1 {
				// The aggregate status already contains the counters.
			} else {
				status = fmt.Sprintf("%s %d/%d", status, stage.current, stage.total)
			}
		} else if stage.current > 0 {
			status = fmt.Sprintf("%s · 已检查 %d 项", status, stage.current)
		}
		mark := frames[frame%len(frames)]
		if strings.HasPrefix(stage.name, "会话轨迹检查完成") {
			mark = "✓"
		}
		lines["trajectory"] = mark + " " + status
	}
	if p.workspaceLatest.name != "" {
		status := p.workspaceLatest.name
		if p.workspaceLatest.total > 0 {
			status = fmt.Sprintf("%s %d/%d", status, p.workspaceLatest.current, p.workspaceLatest.total)
		} else if p.workspaceLatest.current > 0 {
			status = fmt.Sprintf("%s · 已检查 %d 项", status, p.workspaceLatest.current)
		}
		lines["workspace"] = frames[frame%len(frames)] + " " + status
	}
	if p.workspaceTotal > 0 {
		mark := frames[frame%len(frames)]
		if p.workspaceDone >= p.workspaceTotal {
			mark = "✓"
		}
		lines["workspace-summary"] = fmt.Sprintf("%s workspace 完成 %d/%d", mark, p.workspaceDone, p.workspaceTotal)
	}
	if len(lines) == 0 {
		lines["main"] = frames[frame%len(frames)] + " 准备本机扫描"
	}
	return lines
}

func trajectoryCompletionStatus(current, total int, ids []string) string {
	if len(ids) <= 2 {
		return fmt.Sprintf("会话轨迹检查完成 %d/%d · 会话：%s", current, total, strings.Join(ids, "、"))
	}
	last := ids[len(ids)-1]
	if len(last) > 16 {
		last = last[:12] + "…"
	}
	return fmt.Sprintf("会话轨迹检查完成 %d/%d · 已完成 %d 个 · 最近：%s", current, total, len(ids), last)
}

func collectWithProgress(ctx context.Context, u *ui.UI, collector *core.Collector, manifest *core.Manifest) error {
	var progress collectionProgress
	collector.Progress = progress.update
	progressCtx, stop := context.WithCancel(ctx)
	done := make(chan struct{})
	go func() {
		defer close(done)
		ticker := time.NewTicker(120 * time.Millisecond)
		defer ticker.Stop()
		frame := 0
		for {
			select {
			case <-progressCtx.Done():
				return
			case <-ticker.C:
				u.ProgressLines(progress.lines(frame))
				frame++
			}
		}
	}()
	err := collector.Collect(ctx, manifest)
	stop()
	<-done
	// Flush completed stages even if all their updates occurred between ticks.
	u.ProgressLines(progress.lines(0))
	u.ProgressDone()
	return err
}
