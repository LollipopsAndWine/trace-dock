// Command trace-dock-acceptance verifies manifest statistics and optional trees.
package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"sort"
)

type treeEntry struct {
	Type   string `json:"type"`
	Target string `json:"target,omitempty"`
	Size   int64  `json:"size,omitempty"`
	SHA256 string `json:"sha256,omitempty"`
}

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "trace-dock-acceptance:", err)
		os.Exit(1)
	}
}

func run() error {
	baseline := flag.String("baseline", "", "trusted session-start baseline directory")
	restored := flag.String("restored", "", "independently restored directory")
	sessionID := flag.String("session", "", "candidate root session ID")
	flag.Parse()
	if flag.NArg() != 1 {
		return fmt.Errorf("usage: trace-dock-acceptance <manifest.json> [--baseline DIR --restored DIR --session ID]")
	}
	manifestPath, err := filepath.Abs(flag.Arg(0))
	if err != nil {
		return err
	}
	b, err := os.ReadFile(manifestPath)
	if err != nil {
		return err
	}
	var m map[string]any
	if err = json.Unmarshal(b, &m); err != nil {
		return fmt.Errorf("invalid manifest: %w", err)
	}
	result := map[string]any{"participant_id": m["participant_id"], "tool": m["tool"], "job_id": m["job_id"]}
	recorded := asMap(m["summary"])
	roots := asSlice(m["sessions"])
	sessions := []map[string]any{}
	for _, r := range roots {
		flatten(asMap(r), &sessions)
	}
	expected := map[string]int{"selected_count": len(roots), "subagent_count": len(sessions) - len(roots), "workspace_exists_count": 0, "workspace_snapshot_count": 0, "reversible_candidate_count": 0, "reconstructable_count": 0, "archive_only_count": 0, "invalid_count": 0}
	for _, raw := range roots {
		r := asMap(raw)
		w := asMap(r["workspace"])
		if w["exists"] == true {
			expected["workspace_exists_count"]++
		}
		if r["archive_eligibility"] == "candidate" {
			expected["reversible_candidate_count"]++
		}
		if r["reversibility"] == "reconstructable" {
			expected["reconstructable_count"]++
		}
		if r["archive_eligibility"] != "candidate" {
			expected["archive_only_count"]++
		}
		if r["validity"] == "invalid" {
			expected["invalid_count"]++
		}
	}
	for _, a := range asSlice(m["artifacts"]) {
		if asMap(a)["kind"] == "workspace" {
			expected["workspace_snapshot_count"]++
		}
	}
	mismatches := map[string]any{}
	for k, v := range expected {
		if intValue(recorded[k]) != v {
			mismatches[k] = map[string]any{"recorded": recorded[k], "computed": v}
		}
	}
	result["summary"] = recorded
	result["mismatches"] = mismatches
	if anyFlag(*baseline, *restored, *sessionID) {
		if *baseline == "" || *restored == "" || *sessionID == "" {
			return fmt.Errorf("--baseline, --restored and --session must be specified together")
		}
		host, _ := os.Hostname()
		if fmt.Sprint(m["machine_name"]) == host {
			return fmt.Errorf("verification must run on a different machine")
		}
		bdir, _ := filepath.Abs(*baseline)
		rdir, _ := filepath.Abs(*restored)
		if bdir == rdir {
			return fmt.Errorf("baseline and restored must differ")
		}
		var candidate map[string]any
		for _, r := range roots {
			if x := asMap(r); fmt.Sprint(x["session_id"]) == *sessionID {
				candidate = x
				break
			}
		}
		if candidate == nil || candidate["archive_eligibility"] != "candidate" {
			return fmt.Errorf("session is not an unverified candidate")
		}
		expectedTree, err := tree(bdir)
		if err != nil {
			return err
		}
		actualTree, err := tree(rdir)
		if err != nil {
			return err
		}
		diff := []string{}
		keys := map[string]bool{}
		for k := range expectedTree {
			keys[k] = true
		}
		for k := range actualTree {
			keys[k] = true
		}
		all := make([]string, 0, len(keys))
		for k := range keys {
			all = append(all, k)
		}
		sort.Strings(all)
		for _, k := range all {
			if !reflect.DeepEqual(expectedTree[k], actualTree[k]) {
				diff = append(diff, k)
			}
		}
		manifestHash := sha256.Sum256(b)
		result["external_verification"] = map[string]any{"session_id": *sessionID, "verifier_machine": host, "passed": len(diff) == 0, "different_paths": diff, "compared_entries": len(expectedTree), "manifest_sha256": hex.EncodeToString(manifestHash[:])}
	}
	out, _ := json.MarshalIndent(result, "", "  ")
	fmt.Println(string(out))
	if len(mismatches) > 0 {
		return fmt.Errorf("manifest summary mismatch")
	}
	if v, ok := result["external_verification"].(map[string]any); ok && v["passed"] == false {
		return fmt.Errorf("external tree mismatch")
	}
	return nil
}

func asMap(v any) map[string]any {
	m, _ := v.(map[string]any)
	if m == nil {
		return map[string]any{}
	}
	return m
}
func asSlice(v any) []any { s, _ := v.([]any); return s }
func intValue(v any) int {
	switch n := v.(type) {
	case float64:
		return int(n)
	case int:
		return n
	}
	return -1
}
func flatten(s map[string]any, out *[]map[string]any) {
	*out = append(*out, s)
	for _, c := range asSlice(s["children"]) {
		flatten(asMap(c), out)
	}
}
func anyFlag(a, b, c string) bool { return a != "" || b != "" || c != "" }
func tree(root string) (map[string]treeEntry, error) {
	root, err := filepath.Abs(root)
	if err != nil {
		return nil, err
	}
	out := map[string]treeEntry{}
	err = filepath.WalkDir(root, func(path string, d fs.DirEntry, e error) error {
		if e != nil {
			return e
		}
		if path == root {
			return nil
		}
		rel, e := filepath.Rel(root, path)
		if e != nil {
			return e
		}
		rel = filepath.ToSlash(rel)
		if d.Type()&os.ModeSymlink != 0 {
			target, e := os.Readlink(path)
			if e != nil {
				return e
			}
			out[rel] = treeEntry{Type: "symlink", Target: target}
			return nil
		}
		if d.IsDir() {
			out[rel] = treeEntry{Type: "directory"}
			return nil
		}
		info, e := d.Info()
		if e != nil {
			return e
		}
		if !info.Mode().IsRegular() {
			return fmt.Errorf("special file %s", rel)
		}
		f, e := os.Open(path)
		if e != nil {
			return e
		}
		defer f.Close()
		h := sha256.New()
		buf := make([]byte, 1024*1024)
		var n int64
		for {
			r, e := f.Read(buf)
			n += int64(r)
			if r > 0 {
				h.Write(buf[:r])
			}
			if e == io.EOF {
				break
			}
			if e != nil {
				return e
			}
		}
		out[rel] = treeEntry{Type: "file", Size: n, SHA256: hex.EncodeToString(h.Sum(nil))}
		return nil
	})
	return out, err
}
