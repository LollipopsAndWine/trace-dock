package app

import (
	"bufio"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"github.com/trace-dock/trace-dock/internal/api"
	"github.com/trace-dock/trace-dock/internal/core"
	"github.com/trace-dock/trace-dock/internal/ui"
	"github.com/trace-dock/trace-dock/internal/uninstall"
	"github.com/trace-dock/trace-dock/internal/upload"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync/atomic"
	"time"
)

var defaultServer = "https://collector.example.com"

type config struct {
	Server     string `json:"server_url"`
	CodexHome  string `json:"codex_home,omitempty"`
	ClaudeHome string `json:"claude_home,omitempty"`
}
type localJob struct {
	Server      string     `json:"server_url"`
	Participant string     `json:"participant_id"`
	JobID       string     `json:"job_id"`
	Operation   api.Create `json:"create_request"`
}

func Run(ctx context.Context, args []string, version string) error {
	if handled, err := uninstall.HandleInternal(args); handled {
		return err
	}
	if len(args) > 0 && args[0] == "uninstall" {
		return uninstall.Run(args[1:])
	}
	flags := flag.NewFlagSet("trace-dock", flag.ContinueOnError)
	serverFlag := flags.String("server", "", "本次使用的 HTTPS 服务地址")
	codexHomeFlag := flags.String("codex-home", "", "Codex 数据根目录或 sessions 目录")
	claudeHomeFlag := flags.String("claude-home", "", "Claude Code 数据根目录或 projects 目录")
	ver := flags.Bool("version", false, "显示版本")
	flags.Usage = func() {
		fmt.Fprintln(flags.Output(), "trace-dock — 逐项选择 Codex 或 Claude 历史会话并安全归档上传\n用法: trace-dock [--server https://collector.example.com] [--codex-home PATH] [--claude-home PATH] [--version]\n       trace-dock uninstall\n配置: TRACE_DOCK_HOME/config.json；TRACE_DOCK_SERVER；token 仅在终端输入。\n轨迹路径自动读取 CODEX_HOME / CLAUDE_CONFIG_DIR，也可用 --codex-home / --claude-home 显式指定。\n再次运行可恢复已打包任务；恢复使用原选择和归档。uninstall 需要交互确认，只删除 CLI 及 CLI 专属数据。")
	}
	if e := flags.Parse(args); e != nil {
		if e == flag.ErrHelp {
			return nil
		}
		return e
	}
	if *ver {
		fmt.Println(version)
		return nil
	}
	if flags.NArg() != 0 {
		return fmt.Errorf("MVP 仅支持默认交互入口，参见 --help")
	}
	u := ui.New()
	if !u.TTY {
		return fmt.Errorf("需要交互终端；授权和上传必须分别确认")
	}
	u.BeginFlow(fmt.Sprintf("Trace Dock %s · 会话与 workspace 采集", version))
	u.Step("", "连接服务与身份验证")
	home, e := os.UserHomeDir()
	if e != nil {
		return e
	}
	dataHome := os.Getenv("TRACE_DOCK_HOME")
	if dataHome == "" {
		if runtime.GOOS == "windows" {
			dataHome = filepath.Join(os.Getenv("LOCALAPPDATA"), "trace-dock")
		} else {
			dataHome = filepath.Join(home, ".trace-dock")
		}
	}
	if e = core.PrivateDir(dataHome); e != nil {
		return e
	}
	if e = uninstall.MarkManagedData(dataHome); e != nil {
		return e
	}
	cfg := config{Server: defaultServer}
	configPath := filepath.Join(dataHome, "config.json")
	if e = core.ReadJSON(configPath, &cfg); e != nil && !os.IsNotExist(e) {
		return e
	}
	server := cfg.Server
	if v := os.Getenv("TRACE_DOCK_SERVER"); v != "" {
		server = v
	}
	if *serverFlag != "" {
		server = *serverFlag
	}
	chosen, e := u.Line("服务地址（Enter 使用预填地址）", server)
	if e != nil {
		return e
	}
	allowHTTP := os.Getenv("TRACE_DOCK_ALLOW_HTTP") == "1"
	if e = api.ValidURL(chosen, allowHTTP); e != nil {
		return e
	}
	u.Record("服务端", chosen)
	if chosen != server {
		save, e := u.Confirm("保存为新的默认服务地址？")
		if e != nil {
			return e
		}
		if save {
			cfg.Server = chosen
			if e = core.WriteJSON(configPath, cfg); e != nil {
				return e
			}
		}
	}
	server = chosen
	token, e := u.Token()
	if e != nil {
		return e
	}
	if token == "" {
		return fmt.Errorf("token 不能为空")
	}
	client := api.New(server, token)
	identity, e := client.Verify(ctx)
	if e != nil {
		return e
	}
	u.Info(fmt.Sprintf("登记身份：%s（%s）", identity.MaskedEmail, identity.ParticipantID))
	yes, e := u.Confirm("确认这是本人的登记身份？")
	if e != nil {
		return e
	}
	if !yes {
		u.Finish("已取消；没有采集或上传数据。")
		return nil
	}
	u.Record("邀请代码", "验证通过")
	u.Record("身份", identity.MaskedEmail)
	jobs, _ := filepath.Glob(filepath.Join(dataHome, "jobs", "*", "job.json"))
	sort.Strings(jobs)
	for _, p := range jobs {
		var j localJob
		if core.ReadJSON(p, &j) != nil || j.Server != server || j.Participant != identity.ParticipantID {
			continue
		}
		dir := filepath.Dir(p)
		if _, e = os.Stat(filepath.Join(dir, "receipt.json")); e == nil {
			continue
		}
		var savedManifest core.Manifest
		if core.ReadJSON(filepath.Join(dir, "manifest.json"), &savedManifest) != nil || savedManifest.Rules.SecretVersion != core.SecretRulesVersion || savedManifest.Rules.LargeFileVersion != core.LargeFileRulesVersion || len(savedManifest.Rules.SecretPatterns) != 0 {
			continue
		}
		yes, e := u.Confirm("恢复未完成任务 " + j.JobID + "？")
		if e != nil {
			return e
		}
		if yes {
			if _, e = os.Stat(filepath.Join(dir, "collection-complete.json")); os.IsNotExist(e) {
				if e = resumeCollection(ctx, u, client, dir, j.JobID, home); e != nil {
					return e
				}
			}
			return finish(ctx, u, client, dir, j.JobID, allowHTTP)
		}
	}
	u.Step("01/07", "选择采集工具")
	toolIndex, e := u.Choose("选择采集工具", []string{"Codex", "Claude"}, 0)
	if e != nil {
		return e
	}
	tool := []string{"codex", "claude"}[toolIndex]
	devicePath := filepath.Join(dataHome, "device.json")
	var device struct {
		ID string `json:"device_id"`
	}
	if e = core.ReadJSON(devicePath, &device); os.IsNotExist(e) {
		device.ID = core.ID()
		if e = core.WriteJSON(devicePath, device); e != nil {
			return e
		}
	} else if e != nil {
		return e
	}
	machine, e := os.Hostname()
	if e != nil {
		return fmt.Errorf("cannot determine machine name")
	}
	operation := api.Create{OperationID: core.ID(), DeviceID: device.ID, Machine: machine, Tool: tool, Version: version}
	// Persist the creation operation before network I/O so an ambiguous response is retryable.
	pendingPath := filepath.Join(dataHome, "pending-create.json")
	var pending localJob
	if core.ReadJSON(pendingPath, &pending) == nil && pending.Server == server && pending.Participant == identity.ParticipantID && pending.Operation.Tool == tool {
		operation = pending.Operation
	} else {
		pending = localJob{Server: server, Participant: identity.ParticipantID, Operation: operation}
		if e = core.WriteJSON(pendingPath, pending); e != nil {
			return e
		}
	}
	u.Step("02/07", "发现数据并选择上传范围")
	u.Info("正在登记任务并读取候选轨迹元数据；尚未读取 workspace 文件内容。")
	job, e := client.Create(ctx, operation)
	if e != nil {
		return e
	}
	if !core.SafeRelative(job.JobID) || strings.ContainsAny(job.JobID, `/\`) {
		return fmt.Errorf("invalid Job ID")
	}
	if job.Rules.SecretVersion != core.SecretRulesVersion || len(job.Rules.SecretPatterns) != 0 {
		// Do not retry the same idempotent creation after the server rules update:
		// the returned Job has already frozen the unsupported rules.
		_ = client.Patch(ctx, job.JobID, map[string]any{"state": "cancelled"})
		if e = os.Remove(pendingPath); e != nil && !os.IsNotExist(e) {
			return e
		}
		return fmt.Errorf("服务端密钥规则需使用 %s 版 Gitleaks 与明文凭据兜底规则，且不得附加其他检测规则；请更新服务端并重新创建任务", core.SecretRulesVersion)
	}
	if job.Rules.LargeFileVersion != core.LargeFileRulesVersion {
		_ = client.Patch(ctx, job.JobID, map[string]any{"state": "cancelled"})
		if e = os.Remove(pendingPath); e != nil && !os.IsNotExist(e) {
			return e
		}
		return fmt.Errorf("服务端大文件规则需更新为 %s，请重启服务端并重新创建任务", core.LargeFileRulesVersion)
	}
	if job.Rules.ClassificationVersion != "1.0.0" || job.Rules.LargeFileThreshold <= 0 {
		return fmt.Errorf("server returned incomplete versioned rules")
	}
	dir := filepath.Join(dataHome, "jobs", job.JobID)
	pending.JobID = job.JobID
	if e = core.WriteJSON(filepath.Join(dir, "job.json"), pending); e != nil {
		return e
	}
	os.Remove(pendingPath)
	if e = core.Event(dir, "registration", "created", 0); e != nil {
		return e
	}
	u.Record("Job ID", job.JobID)
	u.Info("正在发现候选会话；workspace 文件内容尚未读取。")
	dataFlag := *codexHomeFlag
	if tool == "claude" {
		dataFlag = *claudeHomeFlag
	}
	root, locationSource, e := trajectoryLocation(tool, home, dataFlag, cfg)
	if e != nil {
		return e
	}
	sessions, e := discoverWithLocation(u, tool, home, root, locationSource)
	if e != nil {
		return e
	}
	if e = client.Patch(ctx, job.JobID, map[string]any{"discovered_count": len(sessions)}); e != nil {
		return e
	}
	discovery := discoverySummary(sessions)
	u.Info(discovery...)
	eligible := eligibleSessions(sessions)
	if len(eligible) == 0 {
		_ = client.Patch(ctx, job.JobID, map[string]any{"state": "cancelled"})
		u.Finish("没有可选择的轨迹会话。", "本次没有读取 workspace 内容或上传数据。")
		return nil
	}
	choosePartial, e := u.ChooseScope(sessions)
	if e != nil {
		return e
	}
	selected := eligible
	mode := "all"
	if choosePartial {
		u.Step("03/07", "选择部分会话")
		u.Info(discovery...)
		selected, mode, e = u.Select(sessions)
		if e != nil {
			return e
		}
	}
	u.Step("04/07", "确认所选范围并授权本机采集")
	scopeSummary := selectedScopeSummary(selected, len(eligible))
	u.Info(scopeSummary...)
	yes, e = u.Confirm("授权读取所选轨迹和允许的 workspace、扫描密钥并打包？")
	if e != nil {
		return e
	}
	if !yes {
		_ = client.Patch(ctx, job.JobID, map[string]any{"state": "cancelled"})
		u.Finish("已取消本机采集授权；没有读取 workspace 文件，也没有上传数据。")
		return nil
	}
	now := time.Now().UTC().Truncate(time.Microsecond)
	selection := []string{}
	for _, s := range selected {
		selection = append(selection, s.ID)
	}
	sort.Strings(selection)
	selectionBytes, _ := json.Marshal(selection)
	if !validObjectPrefix(job.ObjectPrefix, job.JobID) {
		return fmt.Errorf("server returned invalid OSS object prefix")
	}
	manifest := &core.Manifest{Schema: core.ManifestSchema, JobID: job.JobID, ParticipantID: identity.ParticipantID, DeviceID: device.ID, Machine: machine, Tool: tool, CLIVersion: version, AdapterVersion: core.AdapterVersion, Rules: job.Rules, ObjectPrefix: job.ObjectPrefix, ConsentedAt: now, SelectionMode: mode, Selection: selection, SelectionSHA256: core.Digest(selectionBytes), Discovered: len(sessions), Sessions: selected, Artifacts: []core.Artifact{}}
	manifest.Selectable = len(eligible)
	if e = core.WriteJSON(filepath.Join(dir, "manifest.json"), manifest); e != nil {
		return e
	}
	if e = client.Patch(ctx, job.JobID, map[string]any{"state": "collecting", "selection_mode": mode, "selected_count": len(selected), "consented_at": now, "selection_sha256": manifest.SelectionSHA256}); e != nil {
		return e
	}
	scanner, e := core.NewScanner()
	if e != nil {
		return fmt.Errorf("密钥规则无效，停止采集")
	}
	collector := core.Collector{Dir: dir, Home: home, Scanner: scanner}
	u.Step("05/07", "扫描、打包并确认上传")
	u.Info(append(scopeSummary, "授权已确认。正在登记选中会话、扫描密钥并打包；尚未上传。")...)
	if e = core.Event(dir, "collection", "consented", len(selected)); e != nil {
		return e
	}
	if e = collectWithProgress(ctx, u, &collector, manifest); e != nil {
		_ = client.Patch(ctx, job.JobID, map[string]any{"state": "resumable"})
		return e
	}
	if e = core.WriteJSON(filepath.Join(dir, "collection-complete.json"), map[string]any{"selection_sha256": manifest.SelectionSHA256}); e != nil {
		return e
	}
	if e = core.Event(dir, "collection", "packaged", len(manifest.Artifacts)); e != nil {
		return e
	}
	if e = client.Patch(ctx, job.JobID, map[string]any{"state": "awaiting_upload_confirmation"}); e != nil {
		return e
	}
	return finish(ctx, u, client, dir, job.JobID, allowHTTP)
}

func validObjectPrefix(prefix, jobID string) bool {
	return strings.HasPrefix(prefix, "trajectory/token=") &&
		strings.Contains(prefix, "/dt=") &&
		strings.HasSuffix(prefix, "/job="+jobID+"/")
}

func selectedScopeSummary(sessions []*core.Session, eligible int) []string {
	policies := map[string]int{}
	paths := map[string]bool{}
	examples := []string{}
	children, crossOwner := 0, 0
	var visit func(*core.Session, bool)
	visit = func(session *core.Session, child bool) {
		if child {
			children++
		}
		if session.Workspace.CrossOwner {
			crossOwner++
		}
		path := session.Workspace.Raw
		if path == "" {
			path = "(未记录 workspace 路径)"
		}
		if !paths[path] {
			paths[path] = true
			policies[session.Workspace.Policy]++
			if len(examples) < 5 {
				examples = append(examples, path+" · "+session.Workspace.Policy)
			}
		}
		for _, nested := range session.Children {
			visit(nested, true)
		}
	}
	for _, session := range sessions {
		visit(session, false)
	}
	lines := []string{fmt.Sprintf("已选 %d / %d 个主会话 · 关联 %d 个子 agent · %d 个 workspace 路径", len(sessions), eligible, children, len(paths))}
	if unselected := eligible - len(sessions); unselected > 0 {
		lines = append(lines, fmt.Sprintf("未选中的 %d 个会话不会读取 workspace 内容、打包或上传。", unselected))
	}
	policyNames := make([]string, 0, len(policies))
	for policy := range policies {
		policyNames = append(policyNames, policy)
	}
	sort.Strings(policyNames)
	for _, policy := range policyNames {
		lines = append(lines, fmt.Sprintf("处理方式：%s %d 个", policy, policies[policy]))
	}
	for _, example := range examples {
		lines = append(lines, ui.WrapLines("  ", safe(example))...)
	}
	if len(paths) > len(examples) {
		lines = append(lines, fmt.Sprintf("  其余 %d 个路径可在授权后查看本机清单。", len(paths)-len(examples)))
	}
	if crossOwner > 0 {
		lines = append(lines, fmt.Sprintf("警示：%d 个会话涉及跨人目录，只记录路径，不读取其内容。", crossOwner))
	}
	lines = append(lines, "即将读取所选轨迹及允许的目录，在本机扫描密钥并打包；此时尚未上传。")
	return lines
}

func resumeCollection(ctx context.Context, u *ui.UI, client *api.Client, dir, id, home string) error {
	var m core.Manifest
	if e := core.ReadJSON(filepath.Join(dir, "manifest.json"), &m); e != nil {
		return e
	}
	if e := validateManifestProtocol(&m); e != nil {
		return e
	}
	if m.Rules.SecretVersion != core.SecretRulesVersion || m.Rules.LargeFileVersion != core.LargeFileRulesVersion || len(m.Rules.SecretPatterns) != 0 {
		return fmt.Errorf("本地任务使用旧版采集规则，请重新创建任务")
	}
	u.Step("04/07", "恢复已确认范围的本机采集")
	u.Info(fmt.Sprintf("上次打包未完成；仅重新处理原先选中的 %d 个根会话及已登记的子 agent。", len(m.Selection)))
	yes, e := u.Confirm("继续授权读取原选择范围并重新扫描打包？")
	if e != nil {
		return e
	}
	if !yes {
		return fmt.Errorf("已保留未完成任务")
	}
	if e = client.Patch(ctx, id, map[string]any{"state": "collecting", "selection_mode": m.SelectionMode, "selected_count": len(m.Selection), "consented_at": m.ConsentedAt, "selection_sha256": m.SelectionSHA256}); e != nil {
		return e
	}
	m.Sessions = core.RefreshSelected(m.Sessions, home)
	u.Step("05/07", "恢复扫描并打包")
	u.Info(append(selectedScopeSummary(m.Sessions, len(m.Selection)), "恢复时只处理原先已确认的会话和 workspace；不会扩大范围。")...)
	m.Artifacts = []core.Artifact{}
	m.Summary = core.Summary{}
	scanner, e := core.NewScanner()
	if e != nil {
		return fmt.Errorf("invalid secret rules")
	}
	c := core.Collector{Dir: dir, Home: home, Scanner: scanner}
	if e = collectWithProgress(ctx, u, &c, &m); e != nil {
		return e
	}
	u.Record("本机采集", "恢复打包完成")
	return core.WriteJSON(filepath.Join(dir, "collection-complete.json"), map[string]any{"selection_sha256": m.SelectionSHA256})
}

func validateManifestProtocol(m *core.Manifest) error {
	if m.Schema != core.ManifestSchema {
		return fmt.Errorf("本地任务使用采集协议 %s，当前版本为 %s；旧任务不能恢复，请新建任务并重新选择会话", m.Schema, core.ManifestSchema)
	}
	return nil
}

func safe(s string) string {
	var b strings.Builder
	for _, r := range s {
		if r >= 32 && r != 127 {
			b.WriteRune(r)
		}
	}
	return b.String()
}

func eligibleSessions(sessions []*core.Session) []*core.Session {
	eligible := make([]*core.Session, 0, len(sessions))
	for _, session := range sessions {
		if session.Eligibility != "excluded" {
			eligible = append(eligible, session)
		}
	}
	return eligible
}

func discoverySummary(sessions []*core.Session) []string {
	reasons := map[string]int{}
	selectable := 0
	for _, session := range sessions {
		if session.Eligibility == "excluded" {
			reason := session.SkipReason
			if reason == "" {
				reason = "unknown"
			}
			reasons[reason]++
		} else {
			selectable++
		}
	}
	lines := []string{fmt.Sprintf("发现 %d 个主会话，其中 %d 个可供选择。", len(sessions), selectable)}
	if len(reasons) == 0 {
		lines[0] += " workspace 文件内容尚未读取。"
		return lines
	}
	keys := make([]string, 0, len(reasons))
	for reason := range reasons {
		keys = append(keys, reason)
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys))
	for _, reason := range keys {
		parts = append(parts, fmt.Sprintf("%s %d", reason, reasons[reason]))
	}
	lines[0] += " workspace 文件内容尚未读取。"
	lines = append(lines, fmt.Sprintf("另有 %d 个无法采集，未纳入选择（%s）。", len(sessions)-selectable, strings.Join(parts, "、")))
	return lines
}

func formatBytes(size int64) string {
	switch {
	case size < 1024:
		return fmt.Sprintf("%d B", size)
	case size < 1<<20:
		return fmt.Sprintf("%.1f KiB", float64(size)/1024)
	default:
		return fmt.Sprintf("%.2f MiB", float64(size)/(1<<20))
	}
}

func exclusionReasonCounts(path string) (map[string]int, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	reasons := map[string]int{}
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		var exclusion core.Exclusion
		if json.Unmarshal(scanner.Bytes(), &exclusion) == nil && exclusion.Reason != "" {
			reasons[exclusion.Reason]++
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	return reasons, nil
}

func exclusionReasonLabel(reason string) string {
	labels := map[string]string{
		"secret_detected":                    "检测到密钥（Gitleaks 或明文凭据兜底规则），文件整体排除",
		"source_unreadable":                  "轨迹文件无法读取",
		"not_regular_or_unreadable":          "轨迹不是普通文件或无法读取",
		"staging_failed":                     "本机暂存失败",
		"scanner_or_copy_failed":             "密钥扫描或复制失败",
		"changed_during_collect":             "采集期间源文件发生变化",
		"cross_owner_requires_authorization": "文件所有者不同，未读取",
		"large_file_type":                    "按文件类型排除正文，仅记路径",
		"large_file_size":                    "超过体积阈值，仅记路径",
		"private_agent_state":                "私有 Agent 状态文件，不采集",
		"unsafe_symbolic_link":               "不安全的符号链接，不采集",
	}
	if label, ok := labels[reason]; ok {
		return label
	}
	return reason
}

func finish(ctx context.Context, u *ui.UI, client *api.Client, dir, id string, allowHTTP bool) error {
	var m core.Manifest
	if e := core.ReadJSON(filepath.Join(dir, "manifest.json"), &m); e != nil {
		return e
	}
	if e := validateManifestProtocol(&m); e != nil {
		return e
	}
	if m.JobID != id {
		return fmt.Errorf("local job identity mismatch")
	}
	if m.Rules.SecretVersion != core.SecretRulesVersion || m.Rules.LargeFileVersion != core.LargeFileRulesVersion || len(m.Rules.SecretPatterns) != 0 {
		return fmt.Errorf("本地任务使用旧版采集规则，请重新创建任务")
	}
	exclusionCounts, e := exclusionReasonCounts(filepath.Join(dir, "exclusions.jsonl"))
	if e != nil {
		return e
	}
	if m.UploadConfirmedAt == nil {
		if e := client.Patch(ctx, id, map[string]any{"state": "awaiting_upload_confirmation"}); e != nil {
			return e
		}
	}
	u.CompactHistory("服务端", "身份", "选择采集工具")
	u.Step("05/07", "核对实际内容并最终确认上传")
	b, e := os.ReadFile(filepath.Join(dir, "manifest.json"))
	if e != nil {
		return e
	}
	inventory := inspectUpload(&m, int64(len(b)))
	u.Info(uploadReviewLines(u, &m, inventory, exclusionCounts, filepath.Join(dir, "manifest.json"))...)
	yes, e := u.Confirm("确认上传以上内容？")
	if e != nil {
		return e
	}
	if !yes {
		u.Finish("已保留本地任务；本次未上传。", "Job ID："+id, "本机清单："+filepath.Join(dir, "manifest.json"))
		return nil
	}
	state, e := upload.Load(dir)
	if os.IsNotExist(e) {
		if m.UploadConfirmedAt == nil {
			t := time.Now().UTC().Truncate(time.Microsecond)
			m.UploadConfirmedAt = &t
			if e = core.WriteJSON(filepath.Join(dir, "manifest.json"), &m); e != nil {
				return e
			}
		}
		state, e = upload.Prepare(dir, &m)
	}
	if e != nil {
		return e
	}
	if m.UploadConfirmedAt == nil || state.SelectionSHA != m.SelectionSHA256 {
		return fmt.Errorf("缺少最终确认或选择范围已变化")
	}
	if e = core.Event(dir, "upload", "confirmed", len(m.Artifacts)); e != nil {
		return e
	}
	uploader := &upload.Uploader{API: client, Dir: dir, JobID: id, AllowHTTP: allowHTTP, State: state}
	var uploadedBytes int64
	allUploaded := len(state.Objects) > 0
	for _, o := range state.Objects {
		if o.Uploaded {
			uploadedBytes += o.Artifact.Size
		} else {
			allUploaded = false
		}
	}
	tool := m.Tool
	if tool == "codex" {
		tool = "Codex"
	} else if tool == "claude" {
		tool = "Claude"
	}
	scopeTotal := m.Discovered
	if m.Selectable > 0 {
		scopeTotal = m.Selectable
	}
	u.BeginUpload(ui.UploadSummary{
		Server: client.Base, Tool: tool,
		Selected: len(m.Selection), Discovered: scopeTotal,
		Subagents: inventory.SelectedSubagents, Workspaces: inventory.Workspaces, Excluded: m.Summary.Excluded,
		ReadyMain: inventory.ReadyMain, ReadySubagents: inventory.ReadySubagents,
		SecretExcluded:       exclusionCounts["secret_detected"],
		LargeFileRecords:     exclusionCounts["large_file_type"] + exclusionCounts["large_file_size"],
		LargeFileTypeRecords: exclusionCounts["large_file_type"],
		UploadedBytes:        uploadedBytes, TotalBytes: state.Bytes(),
	})
	if !allUploaded {
		if e = client.Patch(ctx, id, map[string]any{"state": "uploading", "upload_confirmed_at": m.UploadConfirmedAt}); e != nil {
			u.InterruptUpload()
			return e
		}
		if e = uploadWithProgress(ctx, u, uploader, uploadedBytes); e != nil {
			u.InterruptUpload()
			_ = client.Patch(ctx, id, map[string]any{"state": "resumable"})
			return e
		}
	}
	receipt, e := completeWithProgress(ctx, u, client, id, state.Completion())
	if e != nil {
		return e
	}
	if e = core.WriteJSON(filepath.Join(dir, "receipt.json"), receipt); e != nil {
		return e
	}
	state.Completed = true
	if e = uploader.Save(); e != nil {
		return e
	}
	if e = core.Event(dir, "upload", "verified", len(state.Objects)); e != nil {
		return e
	}
	receiptPath := filepath.Join(dir, "receipt.json")
	u.CompleteCollection(collectionResult(&m, state, client.Base, receiptPath, exclusionCounts))
	return nil
}

func uploadWithProgress(ctx context.Context, u *ui.UI, uploader *upload.Uploader, initialBytes int64) error {
	var transferred atomic.Int64
	transferred.Store(initialBytes)
	uploader.ObjectProgress = func(_, _ int, _ string, _, _, totalTransferred, _ int64) {
		transferred.Store(totalTransferred)
	}
	progressCtx, stop := context.WithCancel(ctx)
	done := make(chan struct{})
	go func() {
		defer close(done)
		ticker := time.NewTicker(120 * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-progressCtx.Done():
				return
			case <-ticker.C:
				u.UploadProgress(transferred.Load())
			}
		}
	}()
	err := uploader.Run(ctx)
	stop()
	<-done
	u.UploadProgress(transferred.Load())
	return err
}
