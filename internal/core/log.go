package core

import (
	"encoding/json"
	"os"
	"path/filepath"
	"time"
)

// Event deliberately accepts no untrusted strings, paths, headers or file content.
func Event(dir, stage, event string, count int) error {
	if e := PrivateDir(dir); e != nil {
		return e
	}
	f, e := os.OpenFile(filepath.Join(dir, "logs.jsonl"), os.O_WRONLY|os.O_CREATE|os.O_APPEND, 0600)
	if e != nil {
		return e
	}
	defer f.Close()
	return json.NewEncoder(f).Encode(map[string]any{"at": time.Now().UTC(), "stage": stage, "event": event, "count": count})
}
