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
	if !strings.Contains(got, "未选中的 5 个会话") || !strings.Contains(got, "其余 15 个路径") {
		t.Fatalf("scope summary should bound path output: %s", got)
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
		{"trajectory/token=opaque/dt=2026-11-14/job=job-123/", true},
		{"trajectory/token=opaque/dt=2026-11-14/", false},
		{"trajectory/token=opaque/dt=2026-11-14/job=other/", false},
		{"collections/job-123/", false},
	} {
		if got := validObjectPrefix(tc.prefix, "job-123"); got != tc.valid {
			t.Errorf("validObjectPrefix(%q) = %v, want %v", tc.prefix, got, tc.valid)
		}
	}
}

func TestManifestProtocolAllowsCurrentVersionAndRejectsOldTask(t *testing.T) {
	if err := validateManifestProtocol(&core.Manifest{Schema: core.ManifestSchema}); err != nil {
		t.Fatalf("current protocol rejected: %v", err)
	}
	err := validateManifestProtocol(&core.Manifest{Schema: "1.0"})
	if err == nil || !strings.Contains(err.Error(), "旧任务不能恢复") || !strings.Contains(err.Error(), "新建任务") {
		t.Fatalf("old protocol did not get a recovery explanation: %v", err)
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
