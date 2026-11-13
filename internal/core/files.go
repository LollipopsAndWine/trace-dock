package core

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
)

func ID() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		panic(err)
	}
	return hex.EncodeToString(b[:])
}
func Digest(b []byte) string { h := sha256.Sum256(b); return hex.EncodeToString(h[:]) }
func HashFile(p string) (string, int64, error) {
	f, e := os.Open(p)
	if e != nil {
		return "", 0, e
	}
	defer f.Close()
	h := sha256.New()
	n, e := io.Copy(h, f)
	return hex.EncodeToString(h.Sum(nil)), n, e
}
func WriteJSON(p string, v any) error {
	b, e := json.MarshalIndent(v, "", "  ")
	if e != nil {
		return e
	}
	return WritePrivate(p, append(b, '\n'))
}
func WritePrivate(p string, b []byte) error {
	if e := PrivateDir(filepath.Dir(p)); e != nil {
		return e
	}
	f, e := os.CreateTemp(filepath.Dir(p), ".tmp-*")
	if e != nil {
		return e
	}
	name := f.Name()
	defer os.Remove(name)
	if e = f.Chmod(0600); e == nil {
		_, e = f.Write(b)
	}
	if e == nil {
		e = f.Sync()
	}
	ce := f.Close()
	if e != nil {
		return e
	}
	if ce != nil {
		return ce
	}
	return os.Rename(name, p)
}
func PrivateDir(p string) error {
	if e := os.MkdirAll(p, 0700); e != nil {
		return e
	}
	st, e := os.Lstat(p)
	if e != nil {
		return e
	}
	if !st.IsDir() || st.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("private directory must not be a symbolic link")
	}
	return secureDirectory(p)
}
func ReadJSON(p string, v any) error {
	b, e := os.ReadFile(p)
	if e != nil {
		return e
	}
	if e = json.Unmarshal(b, v); e != nil {
		return fmt.Errorf("invalid local JSON: %s", filepath.Base(p))
	}
	return nil
}
func SafeRelative(p string) bool {
	return p != "" && p != "." && !filepath.IsAbs(p) && filepath.VolumeName(p) == "" && p != ".." && len(p) > 0 && !containsUnsafe(p)
}
