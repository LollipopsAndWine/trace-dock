package app

import (
	"fmt"
	"sort"
	"strings"

	"github.com/trace-dock/trace-dock/internal/core"
	"github.com/trace-dock/trace-dock/internal/ui"
	"github.com/trace-dock/trace-dock/internal/upload"
)

func collectionResult(m *core.Manifest, state *upload.State, server, receiptPath string, exclusionCounts map[string]int) ui.CollectionResult {
	inventory := inspectUpload(m, 0)
	return ui.CollectionResult{
		Server: server, JobID: m.JobID, ReceiptPath: receiptPath,
		Discovered: m.Discovered, Selectable: m.Selectable, Selected: len(m.Selection),
		SelectedSubagents: inventory.SelectedSubagents,
		UploadedMain:      inventory.ReadyMain, UploadedSubagents: inventory.ReadySubagents, Workspaces: inventory.Workspaces,
		MainExclusionReasons:  missingTrajectorySummary(inventory.MissingMain),
		ChildExclusionReasons: missingTrajectorySummary(inventory.MissingSubagents),
		OtherExclusionReasons: otherExclusionSummary(exclusionCounts),
		SecretExcluded:        exclusionCounts["secret_detected"], Excluded: m.Summary.Excluded, Invalid: m.Summary.Invalid,
		LargeFileRecords:     exclusionCounts["large_file_type"] + exclusionCounts["large_file_size"],
		LargeFileTypeRecords: exclusionCounts["large_file_type"],
		Objects:              len(state.Objects), UploadedBytes: state.Bytes(), TotalBytes: state.Bytes(),
	}
}

func otherExclusionSummary(counts map[string]int) string {
	keys := make([]string, 0, len(counts))
	for reason, count := range counts {
		if count <= 0 || reason == "secret_detected" || reason == "large_file_type" || reason == "large_file_size" {
			continue
		}
		keys = append(keys, reason)
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys))
	for _, reason := range keys {
		parts = append(parts, fmt.Sprintf("%s %d 项", exclusionReasonLabel(reason), counts[reason]))
	}
	return strings.Join(parts, " · ")
}
