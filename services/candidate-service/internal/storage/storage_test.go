package storage

import (
	"context"
	"io"
	"strings"
	"testing"
)

func TestLocal_SaveOpenDelete(t *testing.T) {
	ctx := context.Background()
	l, err := NewLocal(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	n, err := l.Save(ctx, "candidates/abc/r1.pdf", strings.NewReader("%PDF-1.7 hello"))
	if err != nil || n != 14 {
		t.Fatalf("save: n=%d err=%v", n, err)
	}
	rc, err := l.Open(ctx, "candidates/abc/r1.pdf")
	if err != nil {
		t.Fatal(err)
	}
	b, _ := io.ReadAll(rc)
	rc.Close()
	if string(b) != "%PDF-1.7 hello" {
		t.Fatalf("got %q", b)
	}
	if err := l.Delete(ctx, "candidates/abc/r1.pdf"); err != nil {
		t.Fatal(err)
	}
	if _, err := l.Open(ctx, "candidates/abc/r1.pdf"); err != ErrNotFound {
		t.Fatalf("want ErrNotFound, got %v", err)
	}
}

func TestLocal_KeysStayInsideRoot(t *testing.T) {
	root := t.TempDir()
	l, _ := NewLocal(root)
	// Clean("/"+key) neutralises "..", so this lands inside root, not outside.
	p, err := l.path("../../etc/passwd")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(p, root) {
		t.Fatalf("path %q escaped root %q", p, root)
	}
}
