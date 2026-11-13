// Package upload uses Alibaba Cloud's official OSS Go SDK v2 for every OSS operation.
package upload

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/aliyun/alibabacloud-oss-go-sdk-v2/oss"
	"github.com/aliyun/alibabacloud-oss-go-sdk-v2/oss/credentials"
	"github.com/trace-dock/trace-dock/internal/api"
	"github.com/trace-dock/trace-dock/internal/core"
)

type Object struct {
	Artifact core.Artifact `json:"artifact"`
	Path     string        `json:"path"`
	Uploaded bool          `json:"uploaded"`
}
type State struct {
	ManifestSHA  string   `json:"manifest_sha256"`
	SelectionSHA string   `json:"selection_sha256"`
	Objects      []Object `json:"objects"`
	Completed    bool     `json:"completed"`
}
type Uploader struct {
	API        *api.Client
	JobID, Dir string
	AllowHTTP  bool
	HTTP       *http.Client
	creds      api.Credentials
	client     *oss.Client
	State      *State
	Progress   func(completed, total int, bytes int64)
}

func (u *Uploader) Save() error {
	return core.WriteJSON(filepath.Join(u.Dir, "upload-state.json"), u.State)
}
func (u *Uploader) refresh(ctx context.Context) error {
	c, e := u.API.Credentials(ctx, u.JobID)
	if e != nil {
		return e
	}
	if !strings.HasPrefix(c.Prefix, "trajectory/token=") || !strings.HasSuffix(c.Prefix, "/") || c.AccessKeyID == "" || c.AccessKeySecret == "" || c.SecurityToken == "" {
		return fmt.Errorf("invalid job credentials")
	}
	if c.Region == "" {
		c.Region = "cn-hangzhou"
	}
	if !u.AllowHTTP && !strings.HasPrefix(c.Endpoint, "https://") {
		return fmt.Errorf("OSS endpoint must use HTTPS")
	}
	provider := credentials.NewStaticCredentialsProvider(c.AccessKeyID, c.AccessKeySecret, c.SecurityToken)
	cfg := oss.LoadDefaultConfig().WithRegion(c.Region).WithEndpoint(c.Endpoint).WithCredentialsProvider(provider).WithConnectTimeout(30 * time.Second).WithReadWriteTimeout(5 * time.Minute).WithUsePathStyle(c.PathStyle)
	u.client = oss.NewClient(cfg)
	u.creds = c
	return nil
}
func (u *Uploader) Run(ctx context.Context) error {
	if u.State == nil || u.State.ManifestSHA == "" {
		return fmt.Errorf("missing confirmed upload state")
	}
	if u.HTTP == nil {
		u.HTTP = &http.Client{Timeout: 5 * time.Minute}
	}
	var manifest core.Manifest
	if e := core.ReadJSON(filepath.Join(u.Dir, "manifest.json"), &manifest); e != nil {
		return e
	}
	manifestHash, _, e := core.HashFile(filepath.Join(u.Dir, "manifest.json"))
	if e != nil || manifestHash != u.State.ManifestSHA || manifest.SelectionSHA256 != u.State.SelectionSHA || manifest.JobID != u.JobID || manifest.ObjectPrefix == "" || len(u.State.Objects) != len(manifest.Artifacts)+1 {
		return fmt.Errorf("confirmed manifest or upload scope changed")
	}
	expected := map[string]core.Artifact{}
	for _, a := range manifest.Artifacts {
		if _, ok := expected[a.Key]; ok {
			return fmt.Errorf("duplicate artifact key")
		}
		expected[a.Key] = a
	}
	seen := map[string]bool{}
	for i, o := range u.State.Objects {
		if seen[o.Artifact.Key] {
			return fmt.Errorf("duplicate upload key")
		}
		seen[o.Artifact.Key] = true
		if i == len(u.State.Objects)-1 {
			if o.Path != "manifest.json" || o.Artifact.Key != manifest.ObjectPrefix+"manifest.json" || o.Artifact.SHA256 != manifestHash {
				return fmt.Errorf("manifest object changed")
			}
		} else if a, ok := expected[o.Artifact.Key]; !ok || a.SHA256 != o.Artifact.SHA256 || a.Size != o.Artifact.Size || a.ID != o.Artifact.ID {
			return fmt.Errorf("upload scope changed")
		}
	}
	for _, o := range u.State.Objects {
		if !core.SafeRelative(o.Path) {
			return fmt.Errorf("unsafe staging path")
		}
		h, n, e := core.HashFile(filepath.Join(u.Dir, o.Path))
		if e != nil || h != o.Artifact.SHA256 || n != o.Artifact.Size {
			return fmt.Errorf("本地归档发生变化，拒绝扩大或修改已确认范围")
		}
	}
	if e = u.refresh(ctx); e != nil {
		return e
	}
	for i := range u.State.Objects {
		o := &u.State.Objects[i]
		if o.Uploaded {
			continue
		}
		if e = u.put(ctx, o); e != nil {
			return e
		}
		o.Uploaded = true
		if e = u.Save(); e != nil {
			return e
		}
		if u.Progress != nil {
			var n int64
			for j := 0; j <= i; j++ {
				n += u.State.Objects[j].Artifact.Size
			}
			u.Progress(i+1, len(u.State.Objects), n)
		}
	}
	return nil
}
func (u *Uploader) put(ctx context.Context, o *Object) error {
	p := filepath.Join(u.Dir, o.Path)
	head, e := u.client.HeadObject(ctx, &oss.HeadObjectRequest{Bucket: oss.Ptr(u.creds.Bucket), Key: oss.Ptr(o.Artifact.Key)})
	if e == nil && head.ContentLength == o.Artifact.Size && head.Metadata["sha256"] == o.Artifact.SHA256 {
		return nil
	}
	uploader := oss.NewUploader(u.client, func(opt *oss.UploaderOptions) {
		opt.EnableCheckpoint = true
		opt.CheckpointDir = filepath.Join(u.Dir, "staging", ".oss-checkpoints")
		opt.PartSize = 16 << 20
		opt.ParallelNum = 3
	})
	_, e = uploader.UploadFile(ctx, &oss.PutObjectRequest{Bucket: oss.Ptr(u.creds.Bucket), Key: oss.Ptr(o.Artifact.Key), Metadata: map[string]string{"sha256": o.Artifact.SHA256}}, p)
	return e
}
func Prepare(dir string, m *core.Manifest) (*State, error) {
	b, e := os.ReadFile(filepath.Join(dir, "manifest.json"))
	if e != nil {
		return nil, e
	}
	s := &State{ManifestSHA: core.Digest(b), SelectionSHA: m.SelectionSHA256}
	for _, a := range m.Artifacts {
		p := a.LocalPath
		if p == "" {
			switch a.Kind {
			case "trajectory", "workspace":
				ext := ".jsonl"
				if a.Kind == "workspace" {
					ext = ".tar.zst"
				}
				p = filepath.Join(dir, "staging", a.ID+ext)
			case "large_files":
				p = filepath.Join(dir, "large-files", a.WorkspaceID+".jsonl")
			case "exclusions":
				p = filepath.Join(dir, "exclusions.jsonl")
			}
		}
		rel, e := filepath.Rel(dir, p)
		if e != nil || !core.SafeRelative(rel) {
			return nil, fmt.Errorf("unsafe local artifact")
		}
		s.Objects = append(s.Objects, Object{Artifact: a, Path: rel})
	}
	s.Objects = append(s.Objects, Object{Artifact: core.Artifact{ID: "manifest", Kind: "manifest", Key: m.ObjectPrefix + "manifest.json", Size: int64(len(b)), SHA256: s.ManifestSHA}, Path: "manifest.json"})
	return s, core.WriteJSON(filepath.Join(dir, "upload-state.json"), s)
}
func Load(dir string) (*State, error) {
	var s State
	e := core.ReadJSON(filepath.Join(dir, "upload-state.json"), &s)
	return &s, e
}
func (s *State) Completion() api.Completion {
	o := s.Objects[len(s.Objects)-1]
	return api.Completion{ManifestKey: o.Artifact.Key, ManifestSHA: s.ManifestSHA, ManifestSize: o.Artifact.Size}
}
func (s *State) Bytes() int64 {
	var n int64
	for _, o := range s.Objects {
		n += o.Artifact.Size
	}
	return n
}
