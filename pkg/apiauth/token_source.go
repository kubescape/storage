package apiauth

import (
	"errors"
	"io/fs"
	"os"
	"strings"
	"sync"
	"time"
)

// TokenSource provides a bearer token.
type TokenSource interface {
	Token() (string, error)
}

type fileTokenSource struct {
	path  string
	mu    sync.Mutex
	mtime time.Time
	token string
}

// NewFileTokenSource returns a TokenSource reading a (projected) token file.
// It returns "" with a nil error when the file does not exist, re-reads when
// the file's mtime changes, and keeps the last non-empty token if the file
// becomes empty.
func NewFileTokenSource(path string) TokenSource {
	return &fileTokenSource{path: path}
}

func (f *fileTokenSource) Token() (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	fi, err := os.Stat(f.path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return "", nil
		}
		return "", err
	}
	if f.token != "" && fi.ModTime().Equal(f.mtime) {
		return f.token, nil
	}
	data, err := os.ReadFile(f.path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return "", nil
		}
		return "", err
	}
	f.mtime = fi.ModTime()
	if t := strings.TrimSpace(string(data)); t != "" {
		f.token = t
	}
	return f.token, nil
}
