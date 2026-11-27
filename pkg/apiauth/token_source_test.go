package apiauth

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestFileTokenSource_MissingFileReturnsEmpty(t *testing.T) {
	ts := NewFileTokenSource(filepath.Join(t.TempDir(), "nope"))
	tok, err := ts.Token()
	if err != nil || tok != "" {
		t.Fatalf("got %q, %v", tok, err)
	}
}

func TestFileTokenSource_ReadsAndTrims(t *testing.T) {
	p := filepath.Join(t.TempDir(), "token")
	if err := os.WriteFile(p, []byte(" abc\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	tok, err := NewFileTokenSource(p).Token()
	if err != nil || tok != "abc" {
		t.Fatalf("got %q, %v", tok, err)
	}
}

func TestFileTokenSource_ReloadsOnMtimeChange(t *testing.T) {
	p := filepath.Join(t.TempDir(), "token")
	if err := os.WriteFile(p, []byte("a"), 0o600); err != nil {
		t.Fatal(err)
	}
	ts := NewFileTokenSource(p)
	if tok, _ := ts.Token(); tok != "a" {
		t.Fatalf("got %q", tok)
	}
	fi, err := os.Stat(p)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte("b"), 0o600); err != nil {
		t.Fatal(err)
	}
	later := fi.ModTime().Add(time.Second)
	if err := os.Chtimes(p, later, later); err != nil {
		t.Fatal(err)
	}
	if tok, err := ts.Token(); err != nil || tok != "b" {
		t.Fatalf("got %q, %v", tok, err)
	}
}

func TestFileTokenSource_EmptyFileKeepsLast(t *testing.T) {
	p := filepath.Join(t.TempDir(), "token")
	if err := os.WriteFile(p, []byte("a"), 0o600); err != nil {
		t.Fatal(err)
	}
	ts := NewFileTokenSource(p)
	if tok, _ := ts.Token(); tok != "a" {
		t.Fatalf("got %q", tok)
	}
	fi, _ := os.Stat(p)
	if err := os.WriteFile(p, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	later := fi.ModTime().Add(time.Second)
	if err := os.Chtimes(p, later, later); err != nil {
		t.Fatal(err)
	}
	if tok, err := ts.Token(); err != nil || tok != "a" {
		t.Fatalf("got %q, %v", tok, err)
	}
}
