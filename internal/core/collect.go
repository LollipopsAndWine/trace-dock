package core

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

type Collector struct {
	Dir           string
	Home          string
	Scanner       *Scanner
	Exclusions    []Exclusion
	Progress      func(key, stage string, current, total int)
	progressMu    sync.Mutex
	fileSlotsOnce sync.Once
	fileSlots     chan struct{}
}

type workspaceResult struct {
	index      int
	key        string
	sessions   []*Session
	wid        string
	large      []LargeFile
	exclusions []Exclusion
	changed    bool
	archive    Artifact
	err        error
}

type workspaceCandidate struct {
	rel, absolute string
	info          os.FileInfo
	largeReason   string
}

type workspaceCandidateResult struct {
	changed   bool
	included  bool
	large     *LargeFile
	exclusion string
	err       error
}

func (c *Collector) reportProgress(key, stage string, current, total int) {
	if c.Progress == nil {
		return
	}
	c.progressMu.Lock()
	c.Progress(key, stage, current, total)
	c.progressMu.Unlock()
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
	trajectoryTotal := 0
	for _, root := range m.Sessions {
		trajectoryTotal += len(flatten(root))
	}
	trajectoryDone := 0
	lastTrajectoryID := ""
	for _, root := range m.Sessions {
		for _, s := range flatten(root) {
			if e := ctx.Err(); e != nil {
				return e
			}
			lastTrajectoryID = s.ID
			c.reportProgress("trajectory", fmt.Sprintf("检查会话轨迹 %s", s.ID), trajectoryDone, trajectoryTotal)
			if s.Eligibility == "excluded" {
				trajectoryDone++
				c.reportProgress("trajectory", fmt.Sprintf("检查会话轨迹 %s", s.ID), trajectoryDone, trajectoryTotal)
				continue
			}
			name := "trajectory-" + Digest([]byte(s.Source))[:24]
			dst := filepath.Join(c.Dir, "staging", name+".jsonl")
			changed, reason, digest, size := c.stage(ctx, s.Source, dst, nil, "", "trajectory", nil, true)
			if ctx.Err() != nil {
				return ctx.Err()
			}
			s.Changed = s.Changed || changed
			if reason != "" {
				c.Exclusions = append(c.Exclusions, Exclusion{Path: s.Source, Reason: reason, SessionID: s.ID})
				s.Eligibility = "excluded"
				s.SkipReason = reason
			} else {
				key := fmt.Sprintf("%ssessions/%s/%s/trajectory.jsonl", m.ObjectPrefix, m.Tool, pathID(s.ID))
				a := Artifact{ID: name, Kind: "trajectory", Key: key, Size: size, CRC64: digest, LocalPath: dst, Verification: "pending"}
				a.SessionIDs = []string{s.ID}
				m.Artifacts = append(m.Artifacts, a)
				s.TrajectoryArtifact = a.ID
			}
			// Trajectory and workspace are independent selected inputs. A rejected
			// trajectory must not suppress a separately scanned workspace snapshot.
			if s.Workspace.Policy == "snapshot" {
				workspaces[s.Workspace.Normalized] = append(workspaces[s.Workspace.Normalized], s)
			}
			trajectoryDone++
			c.reportProgress("trajectory", fmt.Sprintf("扫描轨迹 %s", s.ID), trajectoryDone, trajectoryTotal)
		}
	}
	completedLabel := "会话轨迹检查完成"
	if lastTrajectoryID != "" {
		completedLabel += " " + lastTrajectoryID
	}
	c.reportProgress("trajectory", completedLabel, trajectoryDone, trajectoryTotal)
	keys := make([]string, 0, len(workspaces))
	for k := range workspaces {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	results := c.snapshotWorkspaces(ctx, m, keys, workspaces)
	for _, result := range results {
		if result.err != nil {
			return result.err
		}
		sessions, wid, large, changed := result.sessions, result.wid, result.large, result.changed
		c.Exclusions = append(c.Exclusions, result.exclusions...)
		for _, s := range sessions {
			s.WorkspaceArtifact = wid
			s.Changed = s.Changed || changed
		}
		a := result.archive
		for _, s := range sessions {
			a.SessionIDs = append(a.SessionIDs, s.ID)
		}
		m.Artifacts = append(m.Artifacts, a)
		list := filepath.Join(c.Dir, "large-files", wid+".jsonl")
		if e := writeLines(list, large); e != nil {
			return e
		}
		la, e := artifactContext(ctx, wid+"-large-files", "large_files", list, fmt.Sprintf("%sworkspaces/%s/large-files.jsonl", m.ObjectPrefix, wid))
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
	if e := ctx.Err(); e != nil {
		return e
	}
	ex := filepath.Join(c.Dir, "exclusions.jsonl")
	if e := writeLines(ex, c.Exclusions); e != nil {
		return e
	}
	a, e := artifactContext(ctx, "exclusions", "exclusions", ex, m.ObjectPrefix+"exclusions.jsonl")
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

func (c *Collector) snapshotWorkspaces(ctx context.Context, m *Manifest, keys []string, workspaces map[string][]*Session) []workspaceResult {
	results := make([]workspaceResult, len(keys))
	jobs := make(chan int)
	var completed atomic.Int64
	workers := runtime.NumCPU()
	if workers < 1 {
		workers = 1
	}
	if workers > 4 {
		workers = 4
	}
	worker := func() {
		for index := range jobs {
			key := keys[index]
			sessions := workspaces[key]
			wid := "workspace-" + Digest([]byte(key))[:24]
			progressKey := "workspace:" + key
			c.reportProgress(progressKey, fmt.Sprintf("扫描 workspace：%s", key), index, len(keys))
			large, archive, changed, exclusions, err := c.snapshot(ctx, m, sessions, wid, progressKey)
			results[index] = workspaceResult{index: index, key: key, sessions: sessions, wid: wid, large: large, changed: changed, exclusions: exclusions, archive: archive, err: err}
			if err == nil {
				done := completed.Add(1)
				c.reportProgress(progressKey, fmt.Sprintf("完成 workspace：%s", key), int(done), len(keys))
			}
		}
	}
	var group sync.WaitGroup
	group.Add(workers)
	for i := 0; i < workers; i++ {
		go func() {
			defer group.Done()
			worker()
		}()
	}
sendJobs:
	for index := range keys {
		select {
		case jobs <- index:
		case <-ctx.Done():
			for remaining := index; remaining < len(keys); remaining++ {
				results[remaining].err = ctx.Err()
			}
			break sendJobs
		}
	}
	close(jobs)
	group.Wait()
	return results
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
	s.Workspace = Classify(s.Workspace.Raw, home, s.DataHome)
	if s.EndedAt != nil {
		v := now.Sub(*s.EndedAt).Seconds()
		s.Delay = &v
	}
	for _, c := range s.Children {
		register(c, home)
	}
}
func artifact(id, kind, p, key string) (Artifact, error) {
	h, n, e := CRC64File(p)
	return Artifact{ID: id, Kind: kind, Key: key, Size: n, CRC64: h, LocalPath: p, Verification: "pending"}, e
}

func artifactContext(ctx context.Context, id, kind, p, key string) (Artifact, error) {
	f, err := os.Open(p)
	if err != nil {
		return Artifact{}, err
	}
	defer f.Close()
	h := NewCRC64()
	n, err := copyContext(ctx, h, f)
	if err != nil {
		return Artifact{}, err
	}
	return Artifact{ID: id, Kind: kind, Key: key, Size: n, CRC64: CRC64String(h), LocalPath: p, Verification: "pending"}, nil
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

func (c *Collector) acquireFileScanSlot(ctx context.Context) (func(), error) {
	c.fileSlotsOnce.Do(func() {
		workers := min(max(runtime.NumCPU(), 1), 4)
		c.fileSlots = make(chan struct{}, workers)
	})
	select {
	case c.fileSlots <- struct{}{}:
		return func() { <-c.fileSlots }, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func (c *Collector) stage(ctx context.Context, src, dst string, root *os.Root, rel, progressKey string, progress func(int64, int64), hashOutput bool) (bool, string, string, int64) {
	changed := false
	for attempt := 0; attempt < 2; attempt++ {
		if e := os.MkdirAll(filepath.Dir(dst), 0700); e != nil {
			return changed, "staging_failed", "", 0
		}
		before, e := os.Lstat(src)
		if e != nil || !before.Mode().IsRegular() {
			return changed, "not_regular_or_unreadable", "", 0
		}
		var in *os.File
		if root != nil {
			in, e = root.Open(rel)
		} else {
			in, e = os.Open(src)
		}
		if e != nil {
			return changed, "source_unreadable", "", 0
		}
		actual, e := in.Stat()
		if e != nil || !os.SameFile(before, actual) {
			in.Close()
			return true, "changed_during_collect", "", 0
		}
		out, e := os.OpenFile(dst, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0600)
		if e != nil {
			in.Close()
			return changed, "staging_failed", "", 0
		}
		destination := io.Writer(out)
		contentHash := NewCRC64()
		if hashOutput {
			destination = io.MultiWriter(out, contentHash)
		}
		release, slotErr := c.acquireFileScanSlot(ctx)
		if slotErr != nil {
			in.Close()
			out.Close()
			os.Remove(dst)
			return changed, "cancelled", "", 0
		}
		hit, se := c.Scanner.CopyContextProgress(ctx, destination, in, func(processed int64) {
			if progress != nil {
				progress(processed, actual.Size())
				return
			}
			c.reportProgress(progressKey, fmt.Sprintf("扫描文件：%s", src), int(processed), int(actual.Size()))
		})
		release()
		after, ae := in.Stat()
		in.Close()
		ce := out.Close()
		if hit || se != nil || ce != nil {
			os.Remove(dst)
			if hit {
				return changed, "secret_detected", "", 0
			}
			if ctx.Err() != nil {
				return changed, "cancelled", "", 0
			}
			return changed, "scanner_or_copy_failed", "", 0
		}
		end, ee := os.Lstat(src)
		if ae != nil || ee != nil || !os.SameFile(before, end) || actual.Size() != after.Size() || !actual.ModTime().Equal(after.ModTime()) || end.Size() != after.Size() || !end.ModTime().Equal(after.ModTime()) {
			changed = true
			os.Remove(dst)
			continue
		}
		digest := ""
		if hashOutput {
			digest = CRC64String(contentHash)
		}
		return changed, "", digest, actual.Size()
	}
	return true, "changed_during_collect", "", 0
}

func (c *Collector) scanOnly(ctx context.Context, src string, root *os.Root, rel, progressKey string, progress func(int64, int64)) (changed, hit bool, reason string) {
	for attempt := 0; attempt < 2; attempt++ {
		if ctx.Err() != nil {
			return changed, false, "cancelled"
		}
		before, err := os.Lstat(src)
		if err != nil || !before.Mode().IsRegular() {
			return changed, false, "not_regular_or_unreadable"
		}
		var input *os.File
		if root != nil {
			input, err = root.Open(rel)
		} else {
			input, err = os.Open(src)
		}
		if err != nil {
			return changed, false, "source_unreadable"
		}
		actual, err := input.Stat()
		if err != nil || !os.SameFile(before, actual) {
			input.Close()
			return true, false, "changed_during_collect"
		}
		release, slotErr := c.acquireFileScanSlot(ctx)
		if slotErr != nil {
			input.Close()
			return changed, false, "cancelled"
		}
		hit, scanErr := c.Scanner.CopyContextProgress(ctx, io.Discard, input, func(processed int64) {
			if progress != nil {
				progress(processed, actual.Size())
				return
			}
			c.reportProgress(progressKey, fmt.Sprintf("扫描大文件：%s", rel), int(processed), int(actual.Size()))
		})
		release()
		after, statErr := input.Stat()
		input.Close()
		if scanErr != nil {
			if ctx.Err() != nil {
				return changed, false, "cancelled"
			}
			return changed, false, "scanner_or_copy_failed"
		}
		if hit {
			return changed, true, ""
		}
		end, endErr := os.Lstat(src)
		if statErr != nil || endErr != nil || !os.SameFile(before, end) || actual.Size() != after.Size() || !actual.ModTime().Equal(after.ModTime()) || end.Size() != after.Size() || !end.ModTime().Equal(after.ModTime()) {
			changed = true
			continue
		}
		return changed, false, ""
	}
	return true, false, "changed_during_collect"
}

func (c *Collector) snapshotLegacy(ctx context.Context, m *Manifest, sessions []*Session, wid, progressKey string) ([]LargeFile, bool, []Exclusion, error) {
	source := sessions[0].Workspace.Raw
	root, e := os.OpenRoot(source)
	if e != nil {
		return nil, false, nil, fmt.Errorf("workspace unavailable after authorization")
	}
	defer root.Close()
	temp, e := os.MkdirTemp(c.Dir, "workspace-staging-")
	if e != nil {
		return nil, false, nil, e
	}
	defer os.RemoveAll(temp)
	var files []string
	var large []LargeFile
	changed := false
	var exclusions []Exclusion
	links := map[string]string{}
	directories := map[string]bool{}
	walked := 0
	e = filepath.WalkDir(source, func(p string, d os.DirEntry, walkErr error) error {
		if e := ctx.Err(); e != nil {
			return e
		}
		walked++
		c.reportProgress(progressKey, fmt.Sprintf("扫描 workspace 文件：%s", source), walked, 0)
		rel, e := filepath.Rel(source, p)
		if e != nil {
			return e
		}
		if rel == "." {
			return nil
		}
		reject := func(reason string) {
			exclusions = append(exclusions, Exclusion{Path: filepath.ToSlash(rel), Reason: reason, WorkspaceID: wid})
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
		if largeReason := LargeFileReason(filepath.ToSlash(rel), st.Size(), m.Rules); largeReason != "" {
			ch, hit, reason := c.scanOnly(ctx, p, root, rel, progressKey, nil)
			if ctx.Err() != nil {
				return ctx.Err()
			}
			changed = changed || ch
			if reason != "" {
				reject(reason)
				return nil
			}
			if hit {
				reject("secret_detected")
				return nil
			}
			remote := remotePath(rel, p, m.Machine, sessions, c.Scanner)
			large = append(large, LargeFile{RelativePath: filepath.ToSlash(rel), RemotePath: remote})
			reject(largeReason)
			return nil
		}
		staged := filepath.Join(temp, rel)
		ch, reason, _, _ := c.stage(ctx, p, staged, root, rel, progressKey, nil, false)
		if ctx.Err() != nil {
			return ctx.Err()
		}
		changed = changed || ch
		if reason != "" {
			reject(reason)
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
		return nil, changed, exclusions, e
	}
	sort.Strings(files)
	dst := filepath.Join(c.Dir, "staging", wid+".tar.gz")
	out, e := os.OpenFile(dst, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0600)
	if e != nil {
		return nil, changed, exclusions, e
	}
	zw := gzip.NewWriter(out)
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
			_, e = copyContextProgress(ctx, tw, f, func(copied int64) {
				c.reportProgress(progressKey, fmt.Sprintf("压缩 workspace 文件：%s", rel), int(copied), int(st.Size()))
			})
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
	return large, changed, exclusions, e
}

func copyContext(ctx context.Context, dst io.Writer, src io.Reader) (int64, error) {
	return copyContextProgress(ctx, dst, src, nil)
}

func copyContextProgress(ctx context.Context, dst io.Writer, src io.Reader, progress func(int64)) (int64, error) {
	buf := make([]byte, 64*1024)
	var total int64
	nextReport := int64(1 << 20)
	for {
		if err := ctx.Err(); err != nil {
			return total, err
		}
		n, err := src.Read(buf)
		if n > 0 {
			total += int64(n)
			if progress != nil && total >= nextReport {
				progress(total)
				nextReport = total + (1 << 20)
			}
			written, writeErr := dst.Write(buf[:n])
			total -= int64(n - written)
			if writeErr != nil {
				return total, writeErr
			}
			if written != n {
				return total, io.ErrShortWrite
			}
		}
		if err == io.EOF {
			return total, nil
		}
		if err != nil {
			return total, err
		}
	}
}

func IsLarge(rel string, n int64, r Rules) bool {
	return LargeFileReason(rel, n, r) != ""
}

// LargeFileReason reports a single primary reason. Explicit type rules precede
// the size threshold; Python .pth path configuration is not a model weight.
func LargeFileReason(rel string, n int64, r Rules) string {
	if rel == ".git" || strings.HasPrefix(rel, ".git/") {
		return ""
	}
	for _, p := range r.Whitelist {
		if match(p, rel) {
			return ""
		}
	}
	for _, p := range r.Blacklist {
		if p == "*.pth" && pythonPathConfiguration(rel) {
			continue
		}
		if match(p, rel) {
			return "large_file_type"
		}
	}
	if n > r.LargeFileThreshold {
		return "large_file_size"
	}
	return ""
}

func pythonPathConfiguration(rel string) bool {
	if path.Ext(rel) != ".pth" {
		return false
	}
	directory := path.Base(path.Dir(rel))
	return directory == "site-packages" || directory == "dist-packages"
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
