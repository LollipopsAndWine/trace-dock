package core

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"fmt"
	"hash"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"
	"time"
)

// snapshot scans files in parallel, then writes the archive in sorted path
// order. The worker phase never writes the final tar, so directory structure
// and archive member ordering remain identical to the sequential implementation.
func (c *Collector) snapshot(ctx context.Context, m *Manifest, sessions []*Session, wid, progressKey string) ([]LargeFile, Artifact, bool, []Exclusion, error) {
	source := sessions[0].Workspace.Raw
	root, err := os.OpenRoot(source)
	if err != nil {
		return nil, Artifact{}, false, nil, fmt.Errorf("workspace unavailable after authorization")
	}
	defer root.Close()
	temp, err := os.MkdirTemp(c.Dir, "workspace-staging-")
	if err != nil {
		return nil, Artifact{}, false, nil, err
	}
	defer os.RemoveAll(temp)

	files := []string{}
	links := map[string]string{}
	directories := map[string]bool{}
	candidates := []workspaceCandidate{}
	exclusions := []Exclusion{}
	walked := 0
	err = filepath.WalkDir(source, func(p string, d os.DirEntry, walkErr error) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		walked++
		c.reportProgress(progressKey, fmt.Sprintf("扫描 workspace 文件：%s", source), walked, 0)
		rel, err := filepath.Rel(source, p)
		if err != nil {
			return err
		}
		reject := func(reason string) {
			exclusions = append(exclusions, Exclusion{Path: filepath.ToSlash(rel), Reason: reason, WorkspaceID: wid})
		}
		if rel == "." {
			return nil
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
			target, err := os.Readlink(p)
			if err != nil {
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
		st, err := d.Info()
		if err != nil || !st.Mode().IsRegular() {
			reject("not_regular_or_unreadable")
			return nil
		}
		candidates = append(candidates, workspaceCandidate{rel: rel, absolute: p, info: st, largeReason: LargeFileReason(filepath.ToSlash(rel), st.Size(), m.Rules)})
		return nil
	})
	if err != nil {
		return nil, Artifact{}, false, exclusions, err
	}
	sort.Slice(candidates, func(i, j int) bool { return candidates[i].rel < candidates[j].rel })
	results := make([]workspaceCandidateResult, len(candidates))
	jobs := make(chan int)
	workerCount := min(max(1, runtime.NumCPU()), 4)
	var workers sync.WaitGroup
	worker := func() {
		defer workers.Done()
		for index := range jobs {
			candidate := candidates[index]
			progress := func(current, total int64) {
				c.reportProgress(progressKey, fmt.Sprintf("扫描 workspace 文件：%s", candidate.rel), int(current), int(total))
			}
			if candidate.largeReason != "" {
				changed, hit, reason := c.scanOnly(ctx, candidate.absolute, root, candidate.rel, progressKey, progress)
				result := workspaceCandidateResult{changed: changed}
				if reason != "" {
					result.exclusion = reason
				} else if hit {
					result.exclusion = "secret_detected"
				} else {
					remote := remotePath(candidate.rel, candidate.absolute, m.Machine, sessions, c.Scanner)
					result.large = &LargeFile{RelativePath: filepath.ToSlash(candidate.rel), RemotePath: remote}
					result.exclusion = candidate.largeReason
				}
				results[index] = result
				continue
			}
			staged := filepath.Join(temp, candidate.rel)
			changed, reason, _, _ := c.stage(ctx, candidate.absolute, staged, root, candidate.rel, progressKey, progress, false)
			results[index] = workspaceCandidateResult{changed: changed, included: reason == "", exclusion: reason}
		}
	}
	workers.Add(workerCount)
	for i := 0; i < workerCount; i++ {
		go worker()
	}
sendJobs:
	for i := range candidates {
		select {
		case jobs <- i:
		case <-ctx.Done():
			break sendJobs
		}
	}
	close(jobs)
	workers.Wait()
	if err := ctx.Err(); err != nil {
		return nil, Artifact{}, false, exclusions, err
	}
	changed := false
	large := []LargeFile{}
	for i, result := range results {
		if result.err != nil {
			return nil, Artifact{}, false, exclusions, result.err
		}
		changed = changed || result.changed
		if result.exclusion != "" {
			exclusions = append(exclusions, Exclusion{Path: filepath.ToSlash(candidates[i].rel), Reason: result.exclusion, WorkspaceID: wid})
		}
		if result.large != nil {
			large = append(large, *result.large)
		}
		if result.included {
			staged := filepath.Join(temp, candidates[i].rel)
			if candidates[i].info.Mode()&0111 != 0 {
				if err := os.Chmod(staged, 0700); err != nil {
					return nil, Artifact{}, false, exclusions, err
				}
			}
			files = append(files, candidates[i].rel)
		}
	}
	sort.Strings(files)
	sort.SliceStable(exclusions, func(i, j int) bool { return exclusions[i].Path < exclusions[j].Path })

	dst := filepath.Join(c.Dir, "staging", wid+".tar.gz")
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0600)
	if err != nil {
		return nil, Artifact{}, false, exclusions, err
	}
	digest := NewCRC64()
	dw := &digestWriter{w: out, h: digest}
	zw := gzip.NewWriter(dw)
	tw := tar.NewWriter(zw)
	for _, rel := range files {
		if err = ctx.Err(); err != nil {
			break
		}
		if directories[rel] {
			err = tw.WriteHeader(&tar.Header{Name: filepath.ToSlash(rel) + "/", Mode: 0755, Typeflag: tar.TypeDir, ModTime: time.Unix(0, 0), Format: tar.FormatPAX})
		} else if target, ok := links[rel]; ok {
			err = tw.WriteHeader(&tar.Header{Name: filepath.ToSlash(rel), Linkname: filepath.ToSlash(target), Mode: 0777, Typeflag: tar.TypeSymlink, ModTime: time.Unix(0, 0), Format: tar.FormatPAX})
		} else {
			f, openErr := os.Open(filepath.Join(temp, rel))
			err = openErr
			if err == nil {
				st, statErr := f.Stat()
				err = statErr
				if err == nil {
					err = tw.WriteHeader(&tar.Header{Name: filepath.ToSlash(rel), Mode: fileMode(st.Mode()), Size: st.Size(), Typeflag: tar.TypeReg, ModTime: time.Unix(0, 0), Format: tar.FormatPAX})
				}
				if err == nil {
					_, err = copyContextProgress(ctx, tw, f, func(copied int64) {
						c.reportProgress(progressKey, fmt.Sprintf("压缩 workspace 文件：%s", rel), int(copied), int(st.Size()))
					})
				}
				f.Close()
			}
		}
		if err != nil {
			break
		}
	}
	for _, closeFn := range []func() error{tw.Close, zw.Close, out.Close} {
		if closeErr := closeFn(); err == nil {
			err = closeErr
		}
	}
	if err != nil {
		return nil, Artifact{}, changed, exclusions, err
	}
	archive := Artifact{ID: wid, Kind: "workspace", Key: fmt.Sprintf("%sworkspaces/%s/workspace.tar.gz", m.ObjectPrefix, wid), Size: dw.n, CRC64: CRC64String(digest), LocalPath: dst, Verification: "pending", WorkspaceID: wid}
	return large, archive, changed, exclusions, nil
}

type digestWriter struct {
	w io.Writer
	h hash.Hash
	n int64
}

func (w *digestWriter) Write(p []byte) (int, error) {
	n, err := w.w.Write(p)
	if n > 0 {
		_, _ = w.h.Write(p[:n])
		w.n += int64(n)
	}
	return n, err
}

func fileMode(mode os.FileMode) int64 {
	if mode&0111 != 0 {
		return 0755
	}
	return 0644
}
