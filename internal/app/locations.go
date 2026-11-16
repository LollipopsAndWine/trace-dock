package app

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/trace-dock/trace-dock/internal/core"
	"github.com/trace-dock/trace-dock/internal/ui"
)

func trajectoryLocation(tool, home, flagValue string, cfg config) (string, string, error) {
	var configured, nativeEnv, folder string
	switch tool {
	case "codex":
		configured, nativeEnv, folder = cfg.CodexHome, "CODEX_HOME", ".codex"
	case "claude":
		configured, nativeEnv, folder = cfg.ClaudeHome, "CLAUDE_CONFIG_DIR", ".claude"
	default:
		return "", "", fmt.Errorf("unsupported tool")
	}
	traceEnv := "TRACE_DOCK_" + strings.ToUpper(tool) + "_HOME"
	choices := []struct{ value, source string }{
		{flagValue, "--" + tool + "-home"},
		{os.Getenv(traceEnv), traceEnv},
		{configured, "config.json"},
		{os.Getenv(nativeEnv), nativeEnv},
		{filepath.Join(home, folder), "默认数据目录"},
	}
	for _, choice := range choices {
		if choice.value == "" {
			continue
		}
		root, err := normalizeTrajectoryRoot(tool, home, choice.value)
		return root, choice.source, err
	}
	return "", "", fmt.Errorf("missing trajectory directory")
}

func normalizeTrajectoryRoot(tool, home, value string) (string, error) {
	if value == "~" {
		value = home
	} else if strings.HasPrefix(value, "~/") || strings.HasPrefix(value, `~\`) {
		value = filepath.Join(home, value[2:])
	}
	root, err := filepath.Abs(value)
	if err != nil {
		return "", fmt.Errorf("无法定位轨迹数据目录")
	}
	suffix := "sessions"
	if tool == "claude" {
		suffix = "projects"
	}
	if !strings.EqualFold(filepath.Base(root), suffix) {
		root = filepath.Join(root, suffix)
	}
	return root, nil
}

func discoverWithLocation(u *ui.UI, tool, home, root, source string) ([]*core.Session, error) {
	for {
		u.Info(ui.WrapLines("轨迹目录：", root+"（"+source+"）")...)
		stopSpinner := u.StartSpinner("正在扫描本机轨迹数据")
		sessions, err := core.Discover(tool, home, root)
		stopSpinner()
		if err == nil && len(sessions) > 0 {
			u.Record("轨迹目录", root)
			return sessions, nil
		}
		message := "该目录未发现轨迹文件，请指定实际的数据目录。"
		if err != nil {
			message = "无法读取该轨迹目录，请检查路径和权限，或指定其他目录。"
		}
		u.Info(append(ui.WrapLines("轨迹目录：", root), message)...)
		value, err := u.Line("轨迹数据目录（可填工具数据根目录或 sessions/projects；留空结束）", "")
		if err != nil {
			return nil, err
		}
		if strings.TrimSpace(value) == "" {
			return nil, nil
		}
		root, err = normalizeTrajectoryRoot(tool, home, value)
		if err != nil {
			return nil, err
		}
		source = "本次手动指定"
	}
}
