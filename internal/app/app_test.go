package app

import (
	"fmt"
	"os"
	"strings"
	"testing"

	"github.com/trace-dock/trace-dock/internal/core"
)

func TestDiscoverySummaryAggregatesExcludedSessions(t *testing.T) {
	sessions := []*core.Session{
		{ID: "skip-one", Eligibility: "excluded", SkipReason: "trajectory_unreadable"},
		{ID: "skip-two", Eligibility: "excluded", SkipReason: "trajectory_unreadable"},
		{ID: "skip-three", Eligibility: "excluded", SkipReason: "source_missing_or_changed"},
		{ID: "select-one", Eligibility: "archive_only"},
		{ID: "select-unknown", Schema: "unknown", Eligibility: "archive_only"},
	}

	got := strings.Join(discoverySummary(sessions), "\n")
	for _, want := range []string{"5 个主会话", "2 个可供选择", "另有 3 个", "trajectory_unreadable 2", "source_missing_or_changed 1"} {
		if !strings.Contains(got, want) {
			t.Errorf("summary missing %q: %s", want, got)
		}
	}
	for _, privateID := range []string{"skip-one", "skip-two", "skip-three"} {
		if strings.Contains(got, privateID) {
			t.Errorf("summary leaked individual skipped session %q", privateID)
		}
	}
}

func TestWriteUsageUsesSectionsAndAlignedOptions(t *testing.T) {
	var output strings.Builder
	writeUsage(&output, "0.1.4")
	got := output.String()
	sections := []string{"Usage:", "Options:", "Environment:", "Configuration:", "Examples:", "Notes:"}
	last := -1
	for _, section := range sections {
		position := strings.Index(got, section)
		if position <= last {
			t.Fatalf("help section %q is missing or out of order:\n%s", section, got)
		}
		last = position
	}
	if !strings.Contains(got, "trace-dock --allow-http --server http://127.0.0.1:8080") || !strings.Contains(got, "trace-dock uninstall --data") {
		t.Fatalf("help is missing common command examples:\n%s", got)
	}

	options := [][2]string{
		{"  --server URL", "本次使用的服务端地址"},
		{"  --allow-http", "测试环境允许使用 HTTP"},
		{"  --codex-home PATH", "Codex 数据目录或 sessions 目录"},
		{"  --claude-home PATH", "Claude 数据目录或 projects 目录"},
		{"  --version", "显示版本并退出"},
		{"  -h, --help", "显示帮助并退出"},
	}
	descriptionColumn := -1
	for _, option := range options {
		prefix, description := option[0], option[1]
		start := strings.Index(got, prefix)
		if start < 0 {
			t.Fatalf("help is missing option %q:\n%s", prefix, got)
		}
		end := strings.IndexByte(got[start:], '\n')
		if end < 0 {
			end = len(got) - start
		}
		line := got[start : start+end]
		column := strings.Index(line, description)
		if descriptionColumn < 0 {
			descriptionColumn = column
		} else if column != descriptionColumn {
			t.Errorf("option description %q starts at %d, want %d", description, column, descriptionColumn)
		}
	}
}

func TestSelectedScopeShowsBoundedWorkspaceSummary(t *testing.T) {
	sessions := make([]*core.Session, 20)
	for i := range sessions {
		sessions[i] = &core.Session{
			ID: fmt.Sprintf("session-%d", i),
			Workspace: core.Workspace{
				Raw:    fmt.Sprintf("/workspace/%d", i),
				Policy: "snapshot",
			},
		}
	}

	got := strings.Join(selectedScopeSummary(sessions, 25), "\n")
	if !strings.Contains(got, "已选 20 / 25 个主会话 · 关联 0 个子 agent · 20 个 workspace 路径") {
		t.Fatalf("scope summary missing: %s", got)
	}
	if !strings.Contains(got, "未选中的 5 个会话") || !strings.Contains(got, "项目快照（读取并打包符合规则的文件）：20 个路径（列出 5 个）") || !strings.Contains(got, "其余 15 个路径") {
		t.Fatalf("scope summary should bound path output: %s", got)
	}
}

func TestSelectedScopeGroupsWorkspacePathsByPolicy(t *testing.T) {
	sessions := []*core.Session{
		{ID: "snapshot-1", Workspace: core.Workspace{Raw: "/workspace/snapshot-1", Policy: "snapshot"}},
		{ID: "snapshot-2", Workspace: core.Workspace{Raw: "/workspace/snapshot-2", Policy: "snapshot"}},
		{ID: "path-1", Workspace: core.Workspace{Raw: "/workspace/path-1", Policy: "path_only"}},
		{ID: "path-2", Workspace: core.Workspace{Raw: "/workspace/path-2", Policy: "path_only"}},
	}

	got := strings.Join(selectedScopeSummary(sessions, 4), "\n")
	for _, want := range []string{
		"workspace 路径明细（共 4 个，按处理方式分组）：",
		"仅记录路径（不读取目录内容）：2 个路径",
		"项目快照（读取并打包符合规则的文件）：2 个路径",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("grouped workspace summary missing %q: %s", want, got)
		}
	}
	if strings.Index(got, "项目快照（读取并打包符合规则的文件）：2 个路径") > strings.Index(got, "/workspace/snapshot-1") {
		t.Fatalf("snapshot paths should follow their group count: %s", got)
	}
	if strings.Index(got, "仅记录路径（不读取目录内容）：2 个路径") > strings.Index(got, "/workspace/path-1") {
		t.Fatalf("path-only paths should follow their group count: %s", got)
	}
}

func TestFormatBytesDoesNotRoundSmallArchivesToZero(t *testing.T) {
	for input, want := range map[int64]string{
		0:       "0 B",
		512:     "512 B",
		2048:    "2.0 KiB",
		1 << 20: "1.00 MiB",
	} {
		if got := formatBytes(input); got != want {
			t.Errorf("formatBytes(%d) = %q, want %q", input, got, want)
		}
	}
}

func TestValidObjectPrefixRequiresJobScope(t *testing.T) {
	for _, tc := range []struct {
		prefix string
		valid  bool
	}{
		{"trajectory/participant_id=opaque/dt=2026-11-14/job=job-123/", true},
		{"trajectory/participant_id=opaque/dt=2026-11-14/", false},
		{"trajectory/participant_id=opaque/dt=2026-11-14/job=other/", false},
		{"trajectory/token=legacy-token/dt=2026-11-14/job=job-123/", false},
		{"collections/job-123/", false},
	} {
		if got := validObjectPrefix(tc.prefix, "job-123"); got != tc.valid {
			t.Errorf("validObjectPrefix(%q) = %v, want %v", tc.prefix, got, tc.valid)
		}
	}
}

func TestManifestProtocolAllowsCurrentVersionAndRejectsOldTask(t *testing.T) {
	if core.ManifestSchema != "1.2" {
		t.Fatalf("manifest schema = %q, want 1.2", core.ManifestSchema)
	}
	if err := validateManifestProtocol(&core.Manifest{Schema: core.ManifestSchema}); err != nil {
		t.Fatalf("current protocol rejected: %v", err)
	}
	err := validateManifestProtocol(&core.Manifest{Schema: "1.1"})
	if err == nil || !strings.Contains(err.Error(), "旧任务不能恢复") || !strings.Contains(err.Error(), "新建任务") {
		t.Fatalf("old protocol did not get a recovery explanation: %v", err)
	}
}

func TestHTTPOptInWorksAcrossShellsAndFlag(t *testing.T) {
	t.Setenv("TRACE_DOCK_ALLOW_HTTP", "")
	if httpAllowed(false) {
		t.Fatal("HTTP enabled without explicit opt-in")
	}
	t.Setenv("TRACE_DOCK_ALLOW_HTTP", "1")
	if !httpAllowed(false) {
		t.Fatal("HTTP environment opt-in ignored")
	}
	t.Setenv("TRACE_DOCK_ALLOW_HTTP", "")
	if !httpAllowed(true) {
		t.Fatal("HTTP command-line opt-in ignored")
	}
}

func TestExclusionReasonExplainsSecretScan(t *testing.T) {
	if got := exclusionReasonLabel("secret_detected"); !strings.Contains(got, "密钥") || !strings.Contains(got, "整体排除") {
		t.Fatalf("unexpected explanation: %s", got)
	}
}

func TestExclusionReasonCountsOnlyGroupsByReason(t *testing.T) {
	file := t.TempDir() + "/exclusions.jsonl"
	content := "{\"path\":\"/private/trajectory.jsonl\",\"reason\":\"secret_detected\"}\n" +
		"{\"path\":\"/private/workspace/.env\",\"reason\":\"secret_detected\"}\n"
	if err := os.WriteFile(file, []byte(content), 0600); err != nil {
		t.Fatal(err)
	}
	reasons, err := exclusionReasonCounts(file)
	if err != nil {
		t.Fatal(err)
	}
	if len(reasons) != 1 || reasons["secret_detected"] != 2 {
		t.Fatalf("exclusions should be grouped only by reason: %#v", reasons)
	}
}
