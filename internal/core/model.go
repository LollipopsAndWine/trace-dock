package core

import "time"

const ManifestSchema = "1.0"
const AdapterVersion = "1.0.0"

func (s *Session) SubagentCount() int {
	n := 0
	for _, c := range s.Children {
		n += 1 + c.SubagentCount()
	}
	return n
}

type Rules struct {
	ClassificationVersion string   `json:"classification_version"`
	SecretVersion         string   `json:"secret_version"`
	LargeFileVersion      string   `json:"large_file_version"`
	LargeFileThreshold    int64    `json:"large_file_threshold_bytes"`
	Whitelist             []string `json:"whitelist"`
	Blacklist             []string `json:"blacklist"`
	SecretPatterns        []string `json:"secret_patterns"`
}

type Workspace struct {
	Platform       string     `json:"cwd_platform"`
	DirectoryMTime *time.Time `json:"directory_mtime_reference"`
	Raw            string     `json:"cwd_raw"`
	Normalized     string     `json:"cwd_normalized"`
	Exists         bool       `json:"exists"`
	Kind           string     `json:"kind"`
	Reason         string     `json:"reason"`
	Policy         string     `json:"snapshot_policy"`
	CrossOwner     bool       `json:"cross_owner"`
}

type Session struct {
	ParticipantID      string            `json:"participant_id"`
	DeviceID           string            `json:"device_id"`
	Machine            string            `json:"machine_name"`
	HasCompaction      bool              `json:"has_compaction"`
	NonCode            bool              `json:"non_code"`
	Tool               string            `json:"tool"`
	ID                 string            `json:"session_id"`
	RootID             string            `json:"root_session_id"`
	ParentID           string            `json:"parent_session_id,omitempty"`
	Aliases            []string          `json:"aliases"`
	Schema             string            `json:"schema"`
	Source             string            `json:"trajectory_path"`
	Workspace          Workspace         `json:"workspace"`
	EndedAt            *time.Time        `json:"session_ended_at"`
	EndReason          string            `json:"end_time_reason,omitempty"`
	CollectedAt        *time.Time        `json:"collected_at"`
	Delay              *float64          `json:"collection_delay_seconds"`
	Validity           string            `json:"validity"`
	Reversibility      string            `json:"reversibility"`
	Eligibility        string            `json:"archive_eligibility"`
	Evidence           []string          `json:"evidence"`
	SkipReason         string            `json:"skip_reason,omitempty"`
	Events             int               `json:"event_count"`
	Size               int64             `json:"source_size_bytes"`
	Ordinals           []int64           `json:"ordinals,omitempty"`
	ThreadNames        []string          `json:"thread_names,omitempty"`
	Children           []*Session        `json:"children,omitempty"`
	TrajectoryArtifact string            `json:"trajectory_artifact_id,omitempty"`
	WorkspaceArtifact  string            `json:"workspace_artifact_id,omitempty"`
	Changed            bool              `json:"changed_during_collect"`
	Downloads          map[string]string `json:"-"`
	explicitRoot       string
	candidate          bool
}

type Artifact struct {
	ID           string   `json:"artifact_id"`
	Kind         string   `json:"kind"`
	SessionIDs   []string `json:"session_ids,omitempty"`
	WorkspaceID  string   `json:"workspace_artifact_id,omitempty"`
	Key          string   `json:"object_key"`
	Size         int64    `json:"size_bytes"`
	SHA256       string   `json:"sha256"`
	Verification string   `json:"verification"`
	LocalPath    string   `json:"-"`
}

type Exclusion struct {
	Path        string `json:"path"`
	Reason      string `json:"reason"`
	WorkspaceID string `json:"workspace_artifact_id,omitempty"`
	SessionID   string `json:"session_id,omitempty"`
}

type LargeFile struct {
	RelativePath string  `json:"relative_path"`
	RemotePath   *string `json:"remote_path"`
}

type Summary struct {
	Selected        int      `json:"selected_count"`
	Subagents       int      `json:"subagent_count"`
	WorkspaceExists int      `json:"workspace_exists_count"`
	Workspaces      int      `json:"workspace_snapshot_count"`
	Candidates      int      `json:"reversible_candidate_count"`
	Reconstructable int      `json:"reconstructable_count"`
	ArchiveOnly     int      `json:"archive_only_count"`
	Invalid         int      `json:"invalid_count"`
	Excluded        int      `json:"excluded_count"`
	LargeFiles      int      `json:"large_file_count"`
	MissingRemote   int      `json:"large_file_missing_remote_path_count"`
	MissingRatio    *float64 `json:"large_file_missing_remote_path_ratio"`
}

type Manifest struct {
	Schema            string     `json:"manifest_schema"`
	JobID             string     `json:"job_id"`
	ParticipantID     string     `json:"participant_id"`
	DeviceID          string     `json:"device_id"`
	Machine           string     `json:"machine_name"`
	Tool              string     `json:"tool"`
	CLIVersion        string     `json:"cli_version"`
	AdapterVersion    string     `json:"adapter_version"`
	Rules             Rules      `json:"rules"`
	ObjectPrefix      string     `json:"object_prefix"`
	ConsentedAt       time.Time  `json:"consented_at"`
	UploadConfirmedAt *time.Time `json:"upload_confirmed_at"`
	SelectionMode     string     `json:"selection_mode"`
	Selection         []string   `json:"selection"`
	SelectionSHA256   string     `json:"selection_sha256"`
	Discovered        int        `json:"discovered_count"`
	Sessions          []*Session `json:"sessions"`
	Artifacts         []Artifact `json:"artifacts"`
	Summary           Summary    `json:"summary"`
}
