package core

import (
	"bufio"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
)

var uuidSuffix = regexp.MustCompile(`(?i)([0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12})\.jsonl$`)

func str(m map[string]any, k string) string         { v, _ := m[k].(string); return v }
func obj(m map[string]any, k string) map[string]any { v, _ := m[k].(map[string]any); return v }
func arr(m map[string]any, k string) []any          { v, _ := m[k].([]any); return v }
func has(m map[string]any, k string) bool           { _, ok := m[k]; return ok }

// Discovery reads only permitted trajectory files and directory metadata, never workspace content.
func Discover(tool, home, root string) ([]*Session, error) {
	var files []string
	if tool == "codex" {
		matches, e := filepath.Glob(filepath.Join(root, "[0-9][0-9][0-9][0-9]", "[0-9][0-9]", "[0-9][0-9]", "rollout-*.jsonl"))
		if e != nil {
			return nil, e
		}
		files = matches
	} else if tool == "claude" {
		e := filepath.WalkDir(root, func(p string, d os.DirEntry, e error) error {
			if e != nil {
				return nil
			}
			if d.IsDir() {
				return nil
			}
			if d.Type()&os.ModeSymlink == 0 && strings.HasSuffix(d.Name(), ".jsonl") {
				files = append(files, p)
			}
			return nil
		})
		if e != nil && !os.IsNotExist(e) {
			return nil, e
		}
	} else {
		return nil, os.ErrInvalid
	}
	var sessions []*Session
	for _, p := range files {
		st, e := os.Lstat(p)
		if e != nil || !st.Mode().IsRegular() {
			continue
		}
		s := parseSession(tool, p, st.Size(), home)
		sessions = append(sessions, s)
	}
	if tool == "claude" {
		byID := map[string]*Session{}
		for _, s := range sessions {
			byID[s.ID] = s
		}
		var roots []*Session
		for _, s := range sessions {
			if s.ParentID != "" {
				if p := byID[s.ParentID]; p != nil && p != s {
					seen := map[string]bool{s.ID: true}
					cur := p
					cycle := false
					for cur != nil {
						if seen[cur.ID] {
							cycle = true
							break
						}
						seen[cur.ID] = true
						cur = byID[cur.ParentID]
					}
					if !cycle {
						p.Children = append(p.Children, s)
						continue
					}
				}
				s.SkipReason = "orphan_or_cyclic_subagent"
				s.Eligibility = "excluded"
			}
			roots = append(roots, s)
		}
		sessions = roots
	}
	sessions = deduplicate(sessions)
	sort.Slice(sessions, func(i, j int) bool {
		if sessions[i].EndedAt == nil {
			return false
		}
		if sessions[j].EndedAt == nil {
			return true
		}
		return sessions[i].EndedAt.After(*sessions[j].EndedAt)
	})
	return sessions, nil
}

func parseSession(tool, p string, size int64, home string) *Session {
	id := strings.TrimSuffix(filepath.Base(p), ".jsonl")
	s := &Session{Tool: tool, ID: id, RootID: id, Source: p, Size: size, Schema: "unsupported", Validity: "invalid", Reversibility: "unknown", Eligibility: "archive_only", Downloads: map[string]string{}, Aliases: []string{}}
	if tool == "codex" {
		if m := uuidSuffix.FindStringSubmatch(p); m != nil {
			s.ID = m[1]
			s.RootID = s.ID
		} else {
			s.SkipReason = "invalid_codex_filename"
			s.Eligibility = "excluded"
			return s
		}
	}
	if tool == "claude" && filepath.Base(filepath.Dir(p)) == "subagents" && strings.HasPrefix(id, "agent-") {
		s.ParentID = filepath.Base(filepath.Dir(filepath.Dir(p)))
		s.RootID = s.ParentID
	}
	f, e := os.Open(p)
	if e != nil {
		s.SkipReason = "trajectory_unreadable"
		return s
	}
	defer f.Close()
	r := bufio.NewReaderSize(f, 64*1024)
	old, newSchema, known, broken, complete, compacted, uncertain, edit := false, false, false, false, false, false, false, false
	cwd := ""
	for {
		// Bound malformed single events while allowing large normal JSONL records.
		line, e := readEvent(r, 64*1024*1024)
		if len(strings.TrimSpace(string(line))) > 0 {
			var m map[string]any
			if json.Unmarshal(line, &m) != nil || m == nil {
				broken = true
			} else {
				s.Events++
				if v := str(m, "timestamp"); v != "" {
					if t, e := time.Parse(time.RFC3339Nano, v); e == nil {
						if s.EndedAt == nil || t.After(*s.EndedAt) {
							s.EndedAt = &t
						}
					}
				}
				if n, ok := m["ordinal"].(float64); ok {
					s.Ordinals = append(s.Ordinals, int64(n))
				}
				if n := str(m, "thread_name"); n != "" {
					s.ThreadNames = append(s.ThreadNames, n)
				}
				if tool == "codex" {
					typ := str(m, "type")
					payload := obj(m, "payload")
					pt := str(payload, "type")
					if typ == "response_item" {
						old = true
					}
					if pt == "item_completed" || pt == "custom_tool_call" {
						newSchema = true
					}
					switch typ {
					case "session_meta", "turn_context":
						if v := str(payload, "cwd"); v != "" {
							cwd = v
						}
						for _, key := range []string{"root_session_id", "forked_from_id", "resumed_from_id"} {
							if v := str(payload, key); v != "" {
								s.explicitRoot = v
								break
							}
						}
					case "compacted":
						compacted = true
					case "event_msg":
						if pt == "task_complete" {
							complete = true
						}
						if pt == "task_started" {
							complete = false
						}
						if pt == "turn_aborted" || pt == "error" || pt == "timeout" {
							broken = true
						}
					case "response_item":
						if pt == "message" && str(payload, "role") == "assistant" {
							if phase := str(payload, "phase"); phase == "final_answer" {
								complete = true
							}
						}
					}
					if pt == "compacted" {
						compacted = true
					}
					if typ == "response_item" || pt == "item_completed" || pt == "custom_tool_call" {
						name := str(payload, "name")
						if name == "exec" || name == "exec_command" {
							uncertain = true
							extractDownloads(payload, s.Downloads)
						}
					}
				} else {
					typ := str(m, "type")
					switch typ {
					case "user", "assistant", "system", "progress", "file-history-snapshot", "queue-operation", "summary":
						known = true
					default:
						if typ != "" {
							uncertain = true
							s.Evidence = append(s.Evidence, "unrecognized_claude_event")
						}
					}
					if v := str(m, "cwd"); v != "" {
						cwd = v
					}
					if v := str(m, "parentSessionId"); v != "" {
						s.ParentID = v
						s.RootID = v
					}
					for _, key := range []string{"rootSessionId", "forkedFromSessionId", "resumedFromSessionId"} {
						if v := str(m, key); v != "" {
							s.explicitRoot = v
							break
						}
					}
					if typ == "user" {
						complete = false
					}
					msg := obj(m, "message")
					if typ == "assistant" && str(msg, "stop_reason") == "end_turn" {
						complete = true
					}
					if typ == "system" && (str(m, "subtype") == "error" || str(m, "subtype") == "turn_aborted") {
						broken = true
					}
					if str(m, "error") != "" || str(msg, "stop_reason") == "max_tokens" {
						broken = true
					}
					if m["is_error"] == true || m["isInterrupted"] == true {
						broken = true
					}
					for _, v := range arr(msg, "content") {
						c, ok := v.(map[string]any)
						if !ok {
							continue
						}
						if c["is_error"] == true {
							broken = true
						}
						if str(c, "type") == "text" && strings.Contains(str(c, "text"), "[Request interrupted by user") {
							broken = true
						}
						if str(c, "type") == "tool_use" {
							input := obj(c, "input")
							switch str(c, "name") {
							case "Edit":
								if has(input, "old_string") && has(input, "new_string") {
									edit = true
								} else {
									uncertain = true
								}
							case "Write", "NotebookEdit":
								uncertain = true
							case "Bash":
								uncertain = true
								extractDownloads(input, s.Downloads)
							case "Agent", "Task":
								uncertain = true // require all referenced children before considering reversal
							case "Read", "Glob", "Grep", "LS", "TodoWrite":
							default:
								uncertain = true
							}
						}
					}
				}
			}
		}
		if e != nil {
			if e != io.EOF {
				broken = true
			}
			break
		}
	}
	s.Workspace = Classify(cwd, home)
	s.NonCode = s.Workspace.Kind == "artifact"
	if tool == "codex" {
		switch {
		case old && newSchema:
			s.SkipReason = "mixed_codex_schema"
			s.Eligibility = "excluded"
		case old:
			s.Schema = "codex.response_item.v1"
		case newSchema:
			s.Schema = "codex.payload.v2"
		default:
			s.SkipReason = "unsupported_codex_schema"
			s.Eligibility = "excluded"
		}
		s.Reversibility = "not_reconstructable"
		s.Evidence = append(s.Evidence, "missing_starting_baseline")
	} else if known {
		s.Schema = "claude.v1"
	} else {
		s.SkipReason = "unsupported_claude_schema"
		s.Eligibility = "excluded"
	}
	if s.EndedAt == nil {
		s.EndReason = "no_event_timestamp"
	}
	if complete && !broken {
		s.Validity = "valid"
	} else {
		s.Evidence = append(s.Evidence, "incomplete_or_error_termination")
	}
	if broken {
		s.Evidence = append(s.Evidence, "damaged_or_error_events")
	}
	if compacted {
		s.HasCompaction = true
		s.Reversibility = "not_reconstructable"
		s.Evidence = append(s.Evidence, "compacted")
	}
	if uncertain {
		s.Evidence = append(s.Evidence, "unproven_tool_or_shell_writes")
	}
	s.candidate = tool == "claude" && edit && !uncertain && !broken && complete
	if s.candidate {
		s.Evidence = append(s.Evidence, "edit_old_new_present_requires_external_verifier")
	}
	return s
}

func readEvent(r *bufio.Reader, limit int) ([]byte, error) {
	var out []byte
	for {
		b, e := r.ReadSlice('\n')
		if len(out)+len(b) > limit {
			return nil, os.ErrInvalid
		}
		out = append(out, b...)
		if e != bufio.ErrBufferFull {
			return out, e
		}
	}
}

func deduplicate(sessions []*Session) []*Session {
	byID := map[string]*Session{}
	for _, s := range sessions {
		byID[s.ID] = s
	}
	groups := map[string][]*Session{}
	for _, s := range sessions {
		root := s.ID
		seen := map[string]bool{}
		cur := s
		for cur.explicitRoot != "" && !seen[cur.ID] {
			seen[cur.ID] = true
			root = cur.explicitRoot
			next := byID[root]
			if next == nil {
				break
			}
			cur = next
		}
		if seen[root] {
			root = s.ID
		}
		s.RootID = root
		groups[root] = append(groups[root], s)
	}
	var out []*Session
	for _, g := range groups {
		sort.SliceStable(g, func(i, j int) bool {
			a, b := g[i], g[j]
			if a.Events != b.Events {
				return a.Events > b.Events
			}
			if a.Size != b.Size {
				return a.Size > b.Size
			}
			if a.EndedAt == nil {
				return false
			}
			return b.EndedAt == nil || a.EndedAt.After(*b.EndedAt)
		})
		keep := g[0]
		for _, s := range g[1:] {
			keep.Aliases = append(keep.Aliases, s.ID)
			keep.Children = append(keep.Children, s.Children...)
		}
		seen := map[string]bool{}
		children := keep.Children[:0]
		for _, c := range keep.Children {
			if !seen[c.Source] {
				seen[c.Source] = true
				children = append(children, c)
			}
		}
		keep.Children = children
		out = append(out, keep)
	}
	return out
}

// Extract only explicit command destination pairs; never execute recorded commands.
var downloadRE = regexp.MustCompile(`(?:wget\s+[^\n]*?-O\s+(\S+)\s+(https?://\S+)|curl\s+[^\n]*?-o\s+(\S+)\s+(https?://\S+)|aws\s+s3\s+cp\s+(s3://\S+)\s+(\S+)|scp\s+(\S+:\S+)\s+(\S+))`)

func extractDownloads(m map[string]any, out map[string]string) {
	cmd := str(m, "command")
	if cmd == "" {
		cmd = str(m, "cmd")
	}
	if cmd == "" {
		var args map[string]any
		_ = json.Unmarshal([]byte(str(m, "arguments")), &args)
		cmd = str(args, "cmd")
		if cmd == "" {
			cmd = str(args, "command")
		}
	}
	for _, v := range downloadRE.FindAllStringSubmatch(cmd, -1) {
		var dst, src string
		switch {
		case v[1] != "":
			dst, src = v[1], v[2]
		case v[3] != "":
			dst, src = v[3], v[4]
		case v[5] != "":
			dst, src = v[6], v[5]
		default:
			dst, src = v[8], v[7]
		}
		out[strings.Trim(dst, `"'`)] = strings.Trim(src, `"'`)
	}
	// Common URL-before-output forms are separate explicit patterns; no shell evaluation.
	for _, r := range []*regexp.Regexp{downloadURLFirstCurl, downloadURLFirstWget} {
		for _, v := range r.FindAllStringSubmatch(cmd, -1) {
			out[strings.Trim(v[2], `"'`)] = strings.Trim(v[1], `"'`)
		}
	}
}

var downloadURLFirstCurl = regexp.MustCompile(`curl\s+(?:-[a-zA-Z]+\s+)*(https?://[^\s"']+)\s+-o\s+("[^"]+"|'[^']+'|[^\s;&|]+)`)
var downloadURLFirstWget = regexp.MustCompile(`wget\s+(?:-[a-zA-Z]+\s+)*(https?://[^\s"']+)\s+-O\s+("[^"]+"|'[^']+'|[^\s;&|]+)`)

// RefreshSelected re-inspects only the original source set after interrupted packaging.
// It never discovers new roots or child agents during recovery.
func RefreshSelected(previous []*Session, home string) []*Session {
	var out []*Session
	for _, old := range previous {
		st, e := os.Lstat(old.Source)
		if e != nil || !st.Mode().IsRegular() {
			old.Eligibility = "excluded"
			old.SkipReason = "source_missing_or_changed"
			old.Children = RefreshSelected(old.Children, home)
			out = append(out, old)
			continue
		}
		s := parseSession(old.Tool, old.Source, st.Size(), home)
		s.ID = old.ID
		s.RootID = old.RootID
		s.ParentID = old.ParentID
		s.Aliases = old.Aliases
		s.Changed = s.Size != old.Size || s.Events != old.Events || ((s.EndedAt == nil) != (old.EndedAt == nil))
		if s.EndedAt != nil && old.EndedAt != nil && !s.EndedAt.Equal(*old.EndedAt) {
			s.Changed = true
		}
		if s.Workspace.Normalized != old.Workspace.Normalized {
			s.Eligibility = "excluded"
			s.SkipReason = "cwd_changed_after_selection"
			s.Workspace = old.Workspace
		}
		s.Children = RefreshSelected(old.Children, home)
		out = append(out, s)
	}
	return out
}
