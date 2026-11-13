package uninstall

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/trace-dock/trace-dock/internal/ui"
)

const cleanupArg = "__trace_dock_cleanup"
const managedMarker = ".trace-dock-managed"

// HandleInternal handles the private post-exit cleanup mode used on Windows.
func HandleInternal(args []string) (bool, error) {
	if len(args) == 0 || args[0] != cleanupArg {
		return false, nil
	}
	if len(args) != 2 {
		return true, fmt.Errorf("invalid uninstall cleanup request")
	}
	current, err := executablePath()
	if err != nil || filepath.Clean(current) != filepath.Clean(args[1]) {
		return true, fmt.Errorf("invalid uninstall executable target")
	}
	return true, cleanupExecutable(args[1])
}

// Run only removes the current CLI executable. User data is deliberately out of scope.
func Run(args []string) error {
	if len(args) != 0 {
		return fmt.Errorf("uninstall 不接受参数")
	}
	if name := os.Getenv("TRACE_DOCK_NPM_PACKAGE"); name != "" {
		return fmt.Errorf("此 CLI 由 npm 管理，请使用 npm uninstall -g %s 卸载；npm 卸载会保留个人配置和任务数据", name)
	}
	u := ui.New()
	if !u.TTY {
		return fmt.Errorf("uninstall 需要交互终端确认")
	}
	executable, err := executablePath()
	if err != nil {
		return err
	}
	data, err := dataHome()
	if err != nil {
		return err
	}
	if err := validateDataHome(data); err != nil {
		return err
	}
	u.Printf("将删除 CLI 可执行文件：\n  %s\n", executable)
	u.Printf("将删除 CLI 专属数据目录：\n  %s\n", data)
	u.Printf("不会删除该目录之外的用户文件、源代码、workspace、OSS 对象或服务端数据。\n")
	ok, err := u.Confirm("确认删除 trace-dock CLI 可执行文件？")
	if err != nil {
		return err
	}
	if !ok {
		u.Printf("已取消卸载。\n")
		return nil
	}
	ok, err = u.Confirm("确认删除上述 CLI 配置、任务和本地归档？")
	if err != nil {
		return err
	}
	if !ok {
		u.Printf("已取消卸载。\n")
		return nil
	}
	if err := os.RemoveAll(data); err != nil {
		return fmt.Errorf("无法删除 CLI 专属数据：%w", err)
	}
	if err := scheduleDelete(executable); err != nil {
		return fmt.Errorf("无法安排删除 CLI：%w", err)
	}
	u.Printf("卸载已安排；用户数据保持不变。\n")
	return nil
}

func dataHome() (string, error) {
	if configured := os.Getenv("TRACE_DOCK_HOME"); configured != "" {
		p, err := filepath.Abs(configured)
		if err != nil { return "", fmt.Errorf("无法定位 TRACE_DOCK_HOME：%w", err) }
		return p, nil
	}
	home, err := os.UserHomeDir()
	if err != nil { return "", fmt.Errorf("无法定位用户目录：%w", err) }
	if os.Getenv("LOCALAPPDATA") != "" && os.PathSeparator == '\\' {
		return filepath.Join(os.Getenv("LOCALAPPDATA"), "trace-dock"), nil
	}
	return filepath.Join(home, ".trace-dock"), nil
}

func MarkManagedData(path string) error {
	if err := validateDataHome(path); err != nil { return err }
	marker := filepath.Join(path, managedMarker)
	if _, err := os.Stat(marker); err == nil { return nil } else if !os.IsNotExist(err) { return err }
	b, _ := json.Marshal(map[string]any{"managed_by": "trace-dock", "version": 1})
	return os.WriteFile(marker, append(b, '\n'), 0600)
}

func validateDataHome(path string) error {
	path, err := filepath.Abs(path)
	if err != nil { return err }
	if path == filepath.Dir(path) || path == "." || path == string(filepath.Separator) { return fmt.Errorf("拒绝把根目录作为 CLI 数据目录") }
	st, err := os.Lstat(path)
	if os.IsNotExist(err) { return os.MkdirAll(path, 0700) }
	if err != nil { return err }
	if !st.IsDir() || st.Mode()&os.ModeSymlink != 0 { return fmt.Errorf("CLI 数据目录不是安全的普通目录") }
	return validateDir(path, true)
}

func validateDir(path string, top bool) error {
	entries, err := os.ReadDir(path); if err != nil { return err }
	allowed := map[string]bool{}
	if top { for _, n := range []string{managedMarker, "config.json", "device.json", "pending-create.json", "jobs"} { allowed[n] = true } } else { for _, n := range []string{"job.json", "discovery.json", "selection.json", "consent.json", "manifest.json", "exclusions.jsonl", "collection-complete.json", "upload-state.json", "receipt.json", "logs.jsonl", "large-files", "staging"} { allowed[n] = true } }
	for _, entry := range entries {
		name := entry.Name(); if !allowed[name] { return fmt.Errorf("CLI 数据目录含未知文件 %q，已拒绝删除", filepath.Join(path, name)) }
		info, err := entry.Info(); if err != nil { return err }; if info.Mode()&os.ModeSymlink != 0 { return fmt.Errorf("CLI 数据目录含符号链接 %q，已拒绝删除", filepath.Join(path, name)) }
		if name == "jobs" || name == "large-files" || name == "staging" { if !info.IsDir() { return fmt.Errorf("CLI 数据目录结构异常：%s", name) }; if err := validateChildren(path, name); err != nil { return err } }
	}
	return nil
}

func validateChildren(parent, name string) error {
	path := filepath.Join(parent, name); entries, err := os.ReadDir(path); if err != nil { return err }
	for _, entry := range entries {
		info, err := entry.Info(); if err != nil { return err }; if info.Mode()&os.ModeSymlink != 0 { return fmt.Errorf("CLI 数据目录含符号链接 %q，已拒绝删除", filepath.Join(path, entry.Name())) }
		if name == "jobs" {
			if !info.IsDir() { return fmt.Errorf("jobs 下存在非任务目录 %q，已拒绝删除", entry.Name()) }
			if err := validateDir(filepath.Join(path, entry.Name()), false); err != nil { return err }
		} else if name == "large-files" {
			if info.IsDir() || !strings.HasSuffix(entry.Name(), ".jsonl") { return fmt.Errorf("large-files 下含未知文件 %q，已拒绝删除", entry.Name()) }
		} else {
			if info.IsDir() && entry.Name() != ".oss-checkpoints" { return fmt.Errorf("staging 下含未知目录 %q，已拒绝删除", entry.Name()) }
			if !info.IsDir() && !(strings.HasSuffix(entry.Name(), ".jsonl") || strings.HasSuffix(entry.Name(), ".tar.zst")) { return fmt.Errorf("staging 下含未知文件 %q，已拒绝删除", entry.Name()) }
			if info.IsDir() { if err := validateCheckpointDir(filepath.Join(path, entry.Name())); err != nil { return err } }
		}
	}
	return nil
}

func validateCheckpointDir(path string) error {
	entries, err := os.ReadDir(path); if err != nil { return err }
	for _, entry := range entries { info, err := entry.Info(); if err != nil { return err }; if info.IsDir() || info.Mode()&os.ModeSymlink != 0 || !strings.HasSuffix(entry.Name(), ".json") { return fmt.Errorf("OSS checkpoint 目录含未知文件，已拒绝删除") } }
	return nil
}

func executablePath() (string, error) {
	p, err := os.Executable()
	if err != nil {
		return "", fmt.Errorf("无法定位 CLI 可执行文件：%w", err)
	}
	p, err = filepath.Abs(p)
	if err != nil {
		return "", fmt.Errorf("无法规范化 CLI 路径：%w", err)
	}
	st, err := os.Lstat(p)
	if err != nil {
		return "", fmt.Errorf("无法检查 CLI 文件：%w", err)
	}
	if st.Mode()&os.ModeSymlink != 0 {
		return "", fmt.Errorf("CLI 位于符号链接上，为避免误删请手动删除链接")
	}
	if !st.Mode().IsRegular() {
		return "", fmt.Errorf("CLI 路径不是普通文件，拒绝卸载")
	}
	base := strings.ToLower(filepath.Base(p))
	if base != "trace-dock" && base != "trace-dock.exe" {
		return "", fmt.Errorf("当前程序不是受支持的 trace-dock 安装文件，拒绝卸载")
	}
	return p, nil
}
