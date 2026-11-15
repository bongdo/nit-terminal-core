package session

import (
	"errors"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func newRoots(t *testing.T) (*Roots, string, string) {
	t.Helper()
	base := t.TempDir()
	inside := filepath.Join(base, "work")
	outside := filepath.Join(base, "outside")
	if err := os.MkdirAll(filepath.Join(inside, "sub"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(outside, 0o755); err != nil {
		t.Fatal(err)
	}
	roots, err := ParseRoots(inside)
	if err != nil {
		t.Fatal(err)
	}
	return roots, inside, outside
}

func TestTarget(t *testing.T) {
	roots, inside, outside := newRoots(t)
	home := filepath.Join(inside, "sub")
	file := filepath.Join(inside, "file.txt")
	if err := os.WriteFile(file, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(inside, "escape")
	if err := os.Symlink(outside, link); err != nil {
		t.Fatal(err)
	}

	validName := "abc_DEF-123"
	tooLong := strings.Repeat("a", 41)

	tests := []struct {
		name    string
		query   string
		want    string
		wantDir string
		wantErr error
	}{
		{name: "dir", query: "dir=" + url.QueryEscape(inside), want: NameForDir(inside), wantDir: inside},
		{name: "dir sub", query: "dir=" + url.QueryEscape(filepath.Join(inside, "sub")), want: NameForDir(filepath.Join(inside, "sub")), wantDir: filepath.Join(inside, "sub")},
		{name: "dir dotdot inside", query: "dir=" + url.QueryEscape(inside+"/sub/../sub"), want: NameForDir(filepath.Join(inside, "sub")), wantDir: filepath.Join(inside, "sub")},
		{name: "relative dir", query: "dir=work", wantErr: ErrNotAbsolute},
		{name: "dot dir", query: "dir=.", wantErr: ErrNotAbsolute},
		{name: "outside dir", query: "dir=" + url.QueryEscape(outside), wantErr: ErrOutside},
		{name: "dotdot escape", query: "dir=" + url.QueryEscape(inside+"/../outside"), wantErr: ErrOutside},
		{name: "missing dir", query: "dir=" + url.QueryEscape(filepath.Join(inside, "nope")), wantErr: ErrNotFound},
		{name: "file dir", query: "dir=" + url.QueryEscape(file), wantErr: ErrNotDir},
		{name: "symlink escape", query: "dir=" + url.QueryEscape(link), wantErr: ErrOutside},
		{name: "session", query: "session=" + validName, want: validName, wantDir: home},
		{name: "session with dir", query: "session=" + validName + "&dir=" + url.QueryEscape(inside), want: validName, wantDir: inside},
		{name: "empty session", query: "session=", want: NameForDir(home), wantDir: home},
		{name: "invalid session space", query: "session=bad%20name", wantErr: ErrBadSession},
		{name: "invalid session slash", query: "session=a%2Fb", wantErr: ErrBadSession},
		{name: "invalid session dot", query: "session=a.b", wantErr: ErrBadSession},
		{name: "session too long", query: "session=" + tooLong, wantErr: ErrBadSession},
		{name: "no query", query: "", want: NameForDir(home), wantDir: home},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			values, err := url.ParseQuery(test.query)
			if err != nil {
				t.Fatal(err)
			}
			name, dir, err := Target(values, roots, home)
			if !errors.Is(err, test.wantErr) {
				t.Fatalf("err = %v, want %v", err, test.wantErr)
			}
			if test.wantErr != nil {
				return
			}
			if name != test.want {
				t.Fatalf("name = %q, want %q", name, test.want)
			}
			if dir != test.wantDir {
				t.Fatalf("dir = %q, want %q", dir, test.wantDir)
			}
		})
	}
}

func TestNameForDir(t *testing.T) {
	base := t.TempDir()
	one := filepath.Join(base, "one")
	two := filepath.Join(base, "two")
	if NameForDir(one) != NameForDir(one) {
		t.Fatal("name is not stable")
	}
	if NameForDir(one) == NameForDir(two) {
		t.Fatal("different dirs share a name")
	}
	if !ValidName(NameForDir(one)) {
		t.Fatalf("generated name %q is invalid", NameForDir(one))
	}
}

func TestParseRoots(t *testing.T) {
	first := t.TempDir()
	second := t.TempDir()
	roots, err := ParseRoots(first + string(os.PathListSeparator) + second)
	if err != nil {
		t.Fatal(err)
	}
	if len(roots.Dirs()) != 2 {
		t.Fatalf("dirs = %v", roots.Dirs())
	}
	if _, err := ParseRoots("relative/path"); err == nil {
		t.Fatal("relative root accepted")
	}
	if _, err := ParseRoots(filepath.Join(first, "missing")); err == nil {
		t.Fatal("missing root accepted")
	}
}
