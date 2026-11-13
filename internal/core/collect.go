package core

import (
	"archive/tar"
	"context"
	"encoding/json"
	"fmt"
	"github.com/klauspost/compress/zstd"
	"io"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

type Collector struct {
	Dir        string
	Home       string
	Scanner    *Scanner
	Exclusions []Exclusion
}

func (c *Collector) Collect(ctx context.Context, m *Manifest) error {
	if m.ConsentedAt.IsZero() || len(m.Sessions) == 0 {
		return fmt.Errorf("collection requires explicit consent and selection")
	}
	if m.Rules.LargeFileThreshold <= 0 {
		return fmt.Errorf("invalid versioned large-file threshold")
	}
	m.Sessions = RefreshSelected(m.Sessions, c.Home)
	// Persist selected sources/times before any workspace body is opened.
	for _, s := range m.Sessions {
		register(s, c.Home)
		for _, v := range flatten(s) {
			v.ParticipantID = m.ParticipantID
			v.DeviceID = m.DeviceID
			v.Machine = m.Machine
			v.NonCode = v.Workspace.Kind == "artifact"
		}
	}
	if e := WriteJSON(filepath.Join(c.Dir, "manifest.json"), m); e != nil {
		return e
	}
	if e := WriteJSON(filepath.Join(c.Dir, "selection.json"), m.Selection); e != nil {
		return e
	}
	if e := WriteJSON(filepath.Join(c.Dir, "consent.json"), map[string]any{"consented_at": m.ConsentedAt, "selection_sha256": m.SelectionSHA256}); e != nil {
		return e
	}
	workspaces := map[string][]*Session{}
	for _, root := range m.Sessions {
		for _, s := range flatten(root) {
			if e := ctx.Err(); e != nil {
				return e
			}
			if s.Eligibility == "excluded" {
				continue
			}
			name := "trajectory-" + Digest([]byte(s.Source))[:24]
			dst := filepath.Join(c.Dir, "staging", name+".jsonl")
			changed, reason := c.stage(s.Source, dst, nil, "")
			s.Changed = s.Changed || changed
			if reason != "" {
				c.Exclusions = append(c.Exclusions, Exclusion{Path: s.Source, Reason: reason, SessionID: s.ID})
				s.Eligibility = "excluded"
				s.SkipReason = reason
				continue
			}
			key := fmt.Sprintf("%ssessions/%s/%s/trajectory.jsonl", m.ObjectPrefix, m.Tool, pathID(s.ID))
			a, e := artifact(name, "trajectory", dst, key)
			if e != nil {
				return e
			}
			a.SessionIDs = []string{s.ID}
			m.Artifacts = append(m.Artifacts, a)
			s.TrajectoryArtifact = a.ID
			if s.Workspace.Policy == "snapshot" {
				workspaces[s.Workspace.Normalized] = append(workspaces[s.Workspace.Normalized], s)
			}
		}
	}
	keys := make([]string, 0, len(workspaces))
	for k := range workspaces {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		sessions := workspaces[k]
		wid := "workspace-" + Digest([]byte(k))[:24]
		large, changed, e := c.snapshot(ctx, m, sessions, wid)
		if e != nil {
			return e
		}
		for _, s := range sessions {
			s.WorkspaceArtifact = wid
			s.Changed = s.Changed || changed
		}
		dst := filepath.Join(c.Dir, "staging", wid+".tar.zst")
		a, e := artifact(wid, "workspace", dst, fmt.Sprintf("%ssessions/%s/%s/workspace.tar.zst", m.ObjectPrefix, m.Tool, pathID(sessions[0].ID)))
		if e != nil {
			return e
		}
		a.WorkspaceID = wid
		for _, s := range sessions {
			a.SessionIDs = append(a.SessionIDs, s.ID)
		}
		m.Artifacts = append(m.Artifacts, a)
		list := filepath.Join(c.Dir, "large-files", wid+".jsonl")
		if e = writeLines(list, large); e != nil {
			return e
		}
		la, e := artifact(wid+"-large-files", "large_files", list, fmt.Sprintf("%sworkspaces/%s/large-files.jsonl", m.ObjectPrefix, wid))
		if e != nil {
			return e
		}
		la.WorkspaceID = wid
		m.Artifacts = append(m.Artifacts, la)
		m.Summary.LargeFiles += len(large)
		for _, l := range large {
			if l.RemotePath == nil {
				m.Summary.MissingRemote++
			}
		}
	}
	ex := filepath.Join(c.Dir, "exclusions.jsonl")
	if e := writeLines(ex, c.Exclusions); e != nil {
		return e
	}
	a, e := artifact("exclusions", "exclusions", ex, m.ObjectPrefix+"exclusions.jsonl")
	if e != nil {
		return e
	}
	m.Artifacts = append(m.Artifacts, a)
	m.Summary.Excluded = len(c.Exclusions)
	Summarize(m)
	return WriteJSON(filepath.Join(c.Dir, "manifest.json"), m)
}

func pathID(s string) string {
	if SafeRelative(s) && !strings.ContainsAny(s, `/\`) {
		return s
	}
	return Digest([]byte(s))
}
func flatten(s *Session) []*Session {
	out := []*Session{s}
	for _, child := range s.Children {
		out = append(out, flatten(child)...)
	}
	return out
}
func register(s *Session, home string) {
	now := time.Now().UTC()
	s.CollectedAt = &now
	s.Workspace = Classify(s.Workspace.Raw, home)
	if s.EndedAt != nil {
		v := now.Sub(*s.EndedAt).Seconds()
		s.Delay = &v
	}
	for _, c := range s.Children {
		register(c, home)
	}
}
func artifact(id, kind, p, key string) (Artifact, error) {
	h, n, e := HashFile(p)
	return Artifact{ID: id, Kind: kind, Key: key, Size: n, SHA256: h, LocalPath: p, Verification: "pending"}, e
}
func writeLines[T any](p string, items []T) error {
	var b strings.Builder
	for _, v := range items {
		j, e := json.Marshal(v)
		if e != nil {
			return e
		}
		b.Write(j)
		b.WriteByte('\n')
	}
	return WritePrivate(p, []byte(b.String()))
}

func (c *Collector) stage(src, dst string, root *os.Root, rel string) (bool, string) {
	changed := false
	for attempt := 0; attempt < 2; attempt++ {
		if e := os.MkdirAll(filepath.Dir(dst), 0700); e != nil {
			return changed, "staging_failed"
		}
		before, e := os.Lstat(src)
		if e != nil || !before.Mode().IsRegular() {
			return changed, "not_regular_or_unreadable"
		}
		var in *os.File
		if root != nil {
			in, e = root.Open(rel)
		} else {
			in, e = os.Open(src)
		}
		if e != nil {
			return changed, "source_unreadable"
		}
		actual, e := in.Stat()
		if e != nil || !os.SameFile(before, actual) {
			in.Close()
			return true, "changed_during_collect"
		}
		out, e := os.OpenFile(dst, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0600)
		if e != nil {
			in.Close()
			return changed, "staging_failed"
		}
		hit, se := c.Scanner.Copy(out, in)
		after, ae := in.Stat()
		in.Close()
		ce := out.Close()
		if hit || se != nil || ce != nil {
			os.Remove(dst)
			if hit {
				return changed, "secret_detected"
			}
			return changed, "scanner_or_copy_failed"
		}
		end, ee := os.Lstat(src)
		if ae != nil || ee != nil || !os.SameFile(before, end) || actual.Size() != after.Size() || !actual.ModTime().Equal(after.ModTime()) || end.Size() != after.Size() || !end.ModTime().Equal(after.ModTime()) {
			changed = true
			os.Remove(dst)
			continue
		}
		return changed, ""
	}
	return true, "changed_during_collect"
}

func (c *Collector) snapshot(ctx context.Context, m *Manifest, sessions []*Session, wid string) ([]LargeFile, bool, error) {
	source := sessions[0].Workspace.Raw
	root, e := os.OpenRoot(source)
	if e != nil {
		return nil, false, fmt.Errorf("workspace unavailable after authorization")
	}
	defer root.Close()
	temp, e := os.MkdirTemp(c.Dir, "workspace-staging-")
	if e != nil {
		return nil, false, e
	}
	defer os.RemoveAll(temp)
	var files []string
	var large []LargeFile
	changed := false
	links := map[string]string{}
	directories := map[string]bool{}
	e = filepath.WalkDir(source, func(p string, d os.DirEntry, walkErr error) error {
		if e := ctx.Err(); e != nil {
			return e
		}
		rel, e := filepath.Rel(source, p)
		if e != nil {
			return e
		}
		if rel == "." {
			return nil
		}
		reject := func(reason string) {
			c.Exclusions = append(c.Exclusions, Exclusion{Path: filepath.ToSlash(rel), Reason: reason, WorkspaceID: wid})
		}
		if walkErr != nil {
			reject("unreadable")
			return nil
		}
		if !SafeRelative(rel) {
			reject("unsafe_path")
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		// Exclude private agent state even when embedded inside a selected repository.
		lower := strings.ToLower(d.Name())
		if lower == ".codex" || lower == ".claude" || lower == ".trace-dock" || lower == ".credentials.json" || strings.HasPrefix(lower, "logs_2.sqlite") {
			reject("private_agent_state")
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if d.IsDir() {
			directories[rel] = true
			files = append(files, rel)
			return nil
		}
		if d.Type()&os.ModeSymlink != 0 {
			target, e := os.Readlink(p)
			if e != nil {
				reject("unreadable_link")
				return nil
			}
			resolved := filepath.Clean(filepath.Join(filepath.Dir(p), target))
			if filepath.IsAbs(target) || !within(source, resolved) || c.Scanner.Hit([]byte(target)) {
				reject("unsafe_symbolic_link")
				return nil
			}
			links[rel] = target
			files = append(files, rel)
			return nil
		}
		st, e := d.Info()
		if e != nil || !st.Mode().IsRegular() {
			reject("not_regular_or_unreadable")
			return nil
		}
		if !ownedByCurrentUser(p) {
			reject("cross_owner_requires_authorization")
			return nil
		}
		staged := filepath.Join(temp, rel)
		ch, reason := c.stage(p, staged, root, rel)
		changed = changed || ch
		if reason != "" {
			reject(reason)
			return nil
		}
		stagedInfo, e := os.Stat(staged)
		if e != nil {
			return e
		}
		if IsLarge(filepath.ToSlash(rel), stagedInfo.Size(), m.Rules) {
			remote := remotePath(rel, p, m.Machine, sessions, c.Scanner)
			large = append(large, LargeFile{RelativePath: filepath.ToSlash(rel), RemotePath: remote})
			reject("large_file_rule")
			os.Remove(staged)
			return nil
		}
		if st.Mode()&0111 != 0 {
			if e := os.Chmod(staged, 0700); e != nil {
				return e
			}
		}
		files = append(files, rel)
		return nil
	})
	if e != nil {
		return nil, changed, e
	}
	sort.Strings(files)
	dst := filepath.Join(c.Dir, "staging", wid+".tar.zst")
	out, e := os.OpenFile(dst, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0600)
	if e != nil {
		return nil, changed, e
	}
	zw, e := zstd.NewWriter(out, zstd.WithEncoderConcurrency(1), zstd.WithEncoderLevel(zstd.SpeedDefault))
	if e != nil {
		out.Close()
		return nil, changed, e
	}
	tw := tar.NewWriter(zw)
	for _, rel := range files {
		if e = ctx.Err(); e != nil {
			break
		}
		if directories[rel] {
			e = tw.WriteHeader(&tar.Header{Name: filepath.ToSlash(rel) + "/", Mode: 0755, Typeflag: tar.TypeDir, ModTime: time.Unix(0, 0), Format: tar.FormatPAX})
			if e != nil {
				break
			}
			continue
		}
		if target, ok := links[rel]; ok {
			e = tw.WriteHeader(&tar.Header{Name: filepath.ToSlash(rel), Linkname: filepath.ToSlash(target), Mode: 0777, Typeflag: tar.TypeSymlink, ModTime: time.Unix(0, 0), Format: tar.FormatPAX})
			if e != nil {
				break
			}
			continue
		}
		p := filepath.Join(temp, rel)
		var f *os.File
		f, e = os.Open(p)
		if e != nil {
			break
		}
		st, _ := f.Stat()
		mode := int64(0644)
		if st.Mode()&0111 != 0 {
			mode = 0755
		}
		e = tw.WriteHeader(&tar.Header{Name: filepath.ToSlash(rel), Mode: mode, Size: st.Size(), Typeflag: tar.TypeReg, ModTime: time.Unix(0, 0), Format: tar.FormatPAX})
		if e == nil {
			_, e = io.Copy(tw, f)
		}
		f.Close()
		if e != nil {
			break
		}
	}
	for _, close := range []func() error{tw.Close, zw.Close, out.Close} {
		if ce := close(); e == nil {
			e = ce
		}
	}
	return large, changed, e
}

func IsLarge(rel string, n int64, r Rules) bool {
	if rel == ".git" || strings.HasPrefix(rel, ".git/") {
		return false
	}
	for _, p := range r.Whitelist {
		if match(p, rel) {
			return false
		}
	}
	for _, p := range r.Blacklist {
		if match(p, rel) {
			return true
		}
	}
	return n > r.LargeFileThreshold
}
func match(pattern, rel string) bool {
	if strings.HasSuffix(pattern, "/**") {
		v := strings.TrimSuffix(pattern, "/**")
		for _, part := range strings.Split(rel, "/") {
			if part == v {
				return true
			}
		}
	}
	ok, _ := path.Match(pattern, rel)
	base, _ := path.Match(pattern, path.Base(rel))
	return ok || base
}
func remotePath(rel, absolute, machine string, sessions []*Session, scanner *Scanner) *string {
	for _, s := range sessions {
		for _, key := range []string{filepath.ToSlash(rel), rel, "./" + rel, absolute} {
			if remote := s.Downloads[key]; remote != "" && !scanner.Hit([]byte(remote)) {
				return &remote
			}
		}
	}
	if machine != "" && filepath.IsAbs(absolute) {
		v := machine + ":" + absolute
		if !scanner.Hit([]byte(v)) {
			return &v
		}
	}
	return nil
}

func Summarize(m *Manifest) {
	s := &m.Summary
	s.Selected = len(m.Sessions)
	s.Subagents = 0
	s.WorkspaceExists = 0
	s.Candidates = 0
	s.Invalid = 0
	s.ArchiveOnly = 0
	s.Workspaces = 0
	s.Reconstructable = 0
	for _, a := range m.Artifacts {
		if a.Kind == "workspace" {
			s.Workspaces++
		}
	}
	for _, r := range m.Sessions {
		all := flatten(r)
		for _, v := range all {
			if v.Validity == "invalid" {
				r.Validity = "invalid"
			}
		}
		s.Subagents += len(all) - 1
		if r.Workspace.Exists {
			s.WorkspaceExists++
		}
		candidate := true
		for _, v := range all {
			if v.Validity != "valid" || !v.candidate || v.TrajectoryArtifact == "" || v.WorkspaceArtifact == "" {
				candidate = false
			}
		}
		if candidate {
			r.Eligibility = "candidate"
			s.Candidates++
		} else {
			if r.Eligibility != "excluded" {
				r.Eligibility = "archive_only"
			}
			s.ArchiveOnly++
		}
		if r.Validity != "valid" {
			s.Invalid++
		}
	}
	if s.LargeFiles > 0 {
		v := float64(s.MissingRemote) / float64(s.LargeFiles)
		s.MissingRatio = &v
	} else {
		s.MissingRatio = nil
	}
}
