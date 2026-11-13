package app

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"github.com/trace-dock/trace-dock/internal/api"
	"github.com/trace-dock/trace-dock/internal/core"
	"github.com/trace-dock/trace-dock/internal/uninstall"
	"github.com/trace-dock/trace-dock/internal/ui"
	"github.com/trace-dock/trace-dock/internal/upload"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"time"
)

var defaultServer = "https://collector.example.com"

type config struct {
	Server string `json:"server_url"`
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
	ver := flags.Bool("version", false, "显示版本")
	flags.Usage = func() {
		fmt.Fprintln(flags.Output(), "trace-dock — 逐项选择 Codex 或 Claude 历史会话并安全归档上传\n用法: trace-dock [--server https://collector.example.com] [--version]\n       trace-dock uninstall\n配置: TRACE_DOCK_HOME/config.json；TRACE_DOCK_SERVER；token 仅在终端输入。\n再次运行可恢复已打包任务；恢复使用原选择和归档。uninstall 需要交互确认，只删除 CLI 及 CLI 专属数据。")
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
	if chosen != server {
		save, e := u.Confirm("保存为新的默认服务地址？")
		if e != nil {
			return e
		}
		if save {
			if e = core.WriteJSON(configPath, config{chosen}); e != nil {
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
	u.Printf("身份：%s（%s）\n", identity.MaskedEmail, identity.ParticipantID)
	yes, e := u.Confirm("确认这是本人的登记身份？")
	if e != nil {
		return e
	}
	if !yes {
		return nil
	}
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
		if _, e = os.Stat(filepath.Join(dir, "manifest.json")); e != nil {
			continue
		}
		yes, e := u.Confirm("恢复未完成任务 " + j.JobID + "？")
		if e != nil {
			return e
		}
		if yes {
			if _, e = os.Stat(filepath.Join(dir, "collection-complete.json")); os.IsNotExist(e) {
				if e = resumeCollection(ctx, u, client, dir, j.JobID, home, token); e != nil {
					return e
				}
			}
			return finish(ctx, u, client, dir, j.JobID, allowHTTP)
		}
	}
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
	job, e := client.Create(ctx, operation)
	if e != nil {
		return e
	}
	if !core.SafeRelative(job.JobID) || strings.ContainsAny(job.JobID, `/\`) {
		return fmt.Errorf("invalid Job ID")
	}
	if job.Rules.ClassificationVersion != "1.0.0" || job.Rules.SecretVersion != "1.0.0" || job.Rules.LargeFileVersion != "1.0.0" || job.Rules.LargeFileThreshold <= 0 {
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
	u.Printf("已登记 Job %s，开始本机发现（尚未读取 workspace 文件内容）。\n", job.JobID)
	root := filepath.Join(home, ".codex", "sessions")
	if tool == "claude" {
		root = filepath.Join(home, ".claude", "projects")
	}
	if env := os.Getenv("TRACE_DOCK_" + strings.ToUpper(tool) + "_HOME"); env != "" {
		suffix := "sessions"
		if tool == "claude" {
			suffix = "projects"
		}
		root = filepath.Join(env, suffix)
	}
	sessions, e := core.Discover(tool, home, root)
	if e != nil {
		return e
	}
	if e = client.Patch(ctx, job.JobID, map[string]any{"discovered_count": len(sessions)}); e != nil {
		return e
	}
	for _, s := range sessions {
		if s.Eligibility == "excluded" {
			u.Printf("跳过 %s：%s\n", safe(s.ID), s.SkipReason)
		}
	}
	selected, mode, e := u.Select(sessions)
	if e != nil {
		return e
	}
	for _, s := range selected {
		showScope(u, s)
	}
	yes, e = u.Confirm("授权在本机读取所选轨迹及允许的 workspace，扫描密钥并打包？")
	if e != nil {
		return e
	}
	if !yes {
		_ = client.Patch(ctx, job.JobID, map[string]any{"state": "cancelled"})
		return nil
	}
	now := time.Now().UTC().Truncate(time.Microsecond)
	selection := []string{}
	for _, s := range selected {
		selection = append(selection, s.ID)
	}
	sort.Strings(selection)
	selectionBytes, _ := json.Marshal(selection)
	if !strings.HasPrefix(job.ObjectPrefix, "trajectory/token=") || !strings.HasSuffix(job.ObjectPrefix, "/") {
		return fmt.Errorf("server returned invalid OSS object prefix")
	}
	manifest := &core.Manifest{Schema: core.ManifestSchema, JobID: job.JobID, ParticipantID: identity.ParticipantID, DeviceID: device.ID, Machine: machine, Tool: tool, CLIVersion: version, AdapterVersion: core.AdapterVersion, Rules: job.Rules, ObjectPrefix: job.ObjectPrefix, ConsentedAt: now, SelectionMode: mode, Selection: selection, SelectionSHA256: core.Digest(selectionBytes), Discovered: len(sessions), Sessions: selected, Artifacts: []core.Artifact{}}
	if e = core.WriteJSON(filepath.Join(dir, "manifest.json"), manifest); e != nil {
		return e
	}
	if e = client.Patch(ctx, job.JobID, map[string]any{"state": "collecting", "selection_mode": mode, "selected_count": len(selected), "consented_at": now, "selection_sha256": manifest.SelectionSHA256}); e != nil {
		return e
	}
	scanner, e := core.NewScanner(job.Rules, token)
	if e != nil {
		return fmt.Errorf("密钥规则无效，停止采集")
	}
	collector := core.Collector{Dir: dir, Home: home, Scanner: scanner}
	u.Printf("正在扫描并归档所选范围……\n")
	if e = core.Event(dir, "collection", "consented", len(selected)); e != nil {
		return e
	}
	if e = collector.Collect(ctx, manifest); e != nil {
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

func showScope(u *ui.UI, s *core.Session) {
	u.Printf("  %s · %s · %s (%s)，子 agent %d\n", safe(s.ID), safe(s.Workspace.Raw), s.Workspace.Policy, s.Workspace.Reason, s.SubagentCount())
	if s.Workspace.CrossOwner {
		u.Printf("  警示：跨人目录，无额外授权，仅记录路径。\n")
	}
	for _, c := range s.Children {
		showScope(u, c)
	}
}

func resumeCollection(ctx context.Context, u *ui.UI, client *api.Client, dir, id, home, token string) error {
	var m core.Manifest
	if e := core.ReadJSON(filepath.Join(dir, "manifest.json"), &m); e != nil {
		return e
	}
	u.Printf("上次打包未完成，将只重新处理原先选中的 %d 个根会话及已登记的子 agent。\n", len(m.Selection))
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
	m.Artifacts = []core.Artifact{}
	m.Summary = core.Summary{}
	scanner, e := core.NewScanner(m.Rules, token)
	if e != nil {
		return fmt.Errorf("invalid secret rules")
	}
	c := core.Collector{Dir: dir, Home: home, Scanner: scanner}
	if e = c.Collect(ctx, &m); e != nil {
		return e
	}
	return core.WriteJSON(filepath.Join(dir, "collection-complete.json"), map[string]any{"selection_sha256": m.SelectionSHA256})
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
func finish(ctx context.Context, u *ui.UI, client *api.Client, dir, id string, allowHTTP bool) error {
	var m core.Manifest
	if e := core.ReadJSON(filepath.Join(dir, "manifest.json"), &m); e != nil {
		return e
	}
	if m.JobID != id {
		return fmt.Errorf("local job identity mismatch")
	}
	if m.UploadConfirmedAt == nil {
		if e := client.Patch(ctx, id, map[string]any{"state": "awaiting_upload_confirmation"}); e != nil {
			return e
		}
	}
	var size int64
	for _, a := range m.Artifacts {
		size += a.Size
	}
	u.Printf("最终范围：%d 根会话、%d 子 agent、%d workspace 快照；%.2f MiB；排除 %d 项；无效 %d 个。\n", m.Summary.Selected, m.Summary.Subagents, m.Summary.Workspaces, float64(size)/(1<<20), m.Summary.Excluded, m.Summary.Invalid)
	u.Printf("manifest: %s\n排除清单: %s\n", filepath.Join(dir, "manifest.json"), filepath.Join(dir, "exclusions.jsonl"))
	b, e := os.ReadFile(filepath.Join(dir, "manifest.json"))
	if e != nil {
		return e
	}
	u.Printf("预览 manifest SHA-256: %s\n", core.Digest(b))
	yes, e := u.Confirm("确认将以上范围直接上传 OSS？")
	if e != nil {
		return e
	}
	if !yes {
		u.Printf("已保留本地归档；再次运行可恢复 Job %s。\n", id)
		return nil
	}
	if e = core.Event(dir, "upload", "confirmed", len(m.Artifacts)); e != nil {
		return e
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
	uploader := &upload.Uploader{API: client, Dir: dir, JobID: id, AllowHTTP: allowHTTP, State: state}
	u.Printf("已冻结上传 manifest SHA-256: %s\n", state.ManifestSHA)
	uploader.Progress = func(done, total int, n int64) {
		u.Printf("上传进度 %d/%d 对象 · %.2f MiB\n", done, total, float64(n)/(1<<20))
	}
	allUploaded := len(state.Objects) > 0
	for _, o := range state.Objects {
		if !o.Uploaded {
			allUploaded = false
		}
	}
	if !allUploaded {
		if e = client.Patch(ctx, id, map[string]any{"state": "uploading", "upload_confirmed_at": m.UploadConfirmedAt}); e != nil {
			return e
		}
		if e = uploader.Run(ctx); e != nil {
			_ = client.Patch(ctx, id, map[string]any{"state": "resumable"})
			return e
		}
	}
	var receipt map[string]any
	if e = client.Call(ctx, "POST", "/v1/collections/"+id+"/complete", state.Completion(), &receipt, ""); e != nil {
		return e
	}
	if receipt["state"] != "completed" {
		return fmt.Errorf("服务端尚未完成对象校验，可再次运行恢复")
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
	u.Printf("校验完成。Job %s；成功 %d 个对象，失败 0，排除 %d；已校验 %.2f MiB。\nreceipt: %s\n", id, len(state.Objects), m.Summary.Excluded, float64(state.Bytes())/(1<<20), filepath.Join(dir, "receipt.json"))
	return nil
}
