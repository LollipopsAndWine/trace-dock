package upload

import (
	"context"
	"github.com/trace-dock/trace-dock/internal/api"
	"github.com/trace-dock/trace-dock/internal/core"
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
