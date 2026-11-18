package upload

import (
	"context"
	"github.com/trace-dock/trace-dock/internal/api"
	"github.com/trace-dock/trace-dock/internal/core"
	"os"
	"path/filepath"
	"testing"
)

func TestSDKUploaderRejectsChangedConfirmedScopeBeforeCredentials(t *testing.T) {
	dir := t.TempDir()
	m := &core.Manifest{JobID: "job", SelectionSHA256: "selection", Artifacts: []core.Artifact{}}
	if e := core.WriteJSON(filepath.Join(dir, "manifest.json"), m); e != nil {
		t.Fatal(e)
	}
	s, e := Prepare(dir, m)
	if e != nil {
		t.Fatal(e)
	}
	s.Objects = append(s.Objects, Object{Path: "extra", Artifact: core.Artifact{Key: "collections/job/extra"}})
	u := Uploader{API: api.New("https://api.test", "token"), Dir: dir, JobID: "job", State: s}
	if e = u.Run(context.Background()); e == nil {
		t.Fatal("changed scope was accepted")
	}
}

func TestPrepareRecoversWorkspacePathFromPersistedManifest(t *testing.T) {
	for _, extension := range []string{".tar.gz", ".tar.zst"} {
		t.Run(extension, func(t *testing.T) {
			dir := t.TempDir()
			name := "workspace-test" + extension
			if err := core.WritePrivate(filepath.Join(dir, "staging", name), []byte("archive")); err != nil {
				t.Fatal(err)
			}
			prefix := "trajectory/participant_id=participant-test/dt=2026-11-18/job=job/"
			m := &core.Manifest{
				JobID: "job", ObjectPrefix: prefix,
				Artifacts: []core.Artifact{{
					ID: "workspace-test", Kind: "workspace",
					Key:       prefix + "workspaces/workspace-test/workspace" + extension,
					LocalPath: filepath.Join(dir, "staging", name),
				}},
			}
			manifestPath := filepath.Join(dir, "manifest.json")
			if err := core.WriteJSON(manifestPath, m); err != nil {
				t.Fatal(err)
			}
			var persisted core.Manifest
			if err := core.ReadJSON(manifestPath, &persisted); err != nil {
				t.Fatal(err)
			}
			state, err := Prepare(dir, &persisted)
			if err != nil {
				t.Fatal(err)
			}
			if got := state.Objects[0].Path; got != filepath.Join("staging", name) {
				t.Fatalf("recovered workspace staging path = %q", got)
			}
			if _, err := os.Stat(filepath.Join(dir, state.Objects[0].Path)); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestUploadParallelismDefaultsAndBounds(t *testing.T) {
	for _, test := range []struct {
		value string
		want  int
	}{
		{value: "", want: 6},
		{value: "1", want: 1},
		{value: "12", want: 12},
		{value: "0", want: 6},
		{value: "17", want: 6},
		{value: "invalid", want: 6},
	} {
		t.Setenv("TRACE_DOCK_UPLOAD_PARALLEL", test.value)
		if got := uploadParallelism(); got != test.want {
			t.Errorf("TRACE_DOCK_UPLOAD_PARALLEL=%q: got %d, want %d", test.value, got, test.want)
		}
	}
}
