package session

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
)

var (
	ErrNotAbsolute = errors.New("dir must be an absolute path")
	ErrNotFound    = errors.New("directory does not exist")
	ErrNotDir      = errors.New("path is not a directory")
	ErrOutside     = errors.New("directory is outside the allowed roots")
)

type Roots struct {
	dirs []string
}

func ParseRoots(value string) (*Roots, error) {
	items := strings.Split(value, string(os.PathListSeparator))
	if strings.TrimSpace(value) == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return nil, err
		}
		items = []string{home}
	}
	roots := &Roots{}
	for _, item := range items {
		item = strings.TrimSpace(item)
		if item == "" {
			continue
		}
		if !filepath.IsAbs(item) {
			return nil, errors.New("root must be absolute: " + item)
		}
		resolved, err := filepath.EvalSymlinks(filepath.Clean(item))
		if err != nil {
			return nil, err
		}
		info, err := os.Stat(resolved)
		if err != nil {
			return nil, err
		}
		if !info.IsDir() {
			return nil, errors.New("root is not a directory: " + item)
		}
		roots.dirs = append(roots.dirs, resolved)
	}
	if len(roots.dirs) == 0 {
		return nil, errors.New("no allowed roots")
	}
	return roots, nil
}

func (r *Roots) Resolve(dir string) (string, error) {
	if !filepath.IsAbs(dir) {
		return "", ErrNotAbsolute
	}
	resolved, err := filepath.EvalSymlinks(filepath.Clean(dir))
	if err != nil {
		if os.IsNotExist(err) {
			return "", ErrNotFound
		}
		return "", ErrNotFound
	}
	info, err := os.Stat(resolved)
	if err != nil {
		return "", ErrNotFound
	}
	if !info.IsDir() {
		return "", ErrNotDir
	}
	for _, root := range r.dirs {
		if within(root, resolved) {
			return resolved, nil
		}
	}
	return "", ErrOutside
}

func within(root, path string) bool {
	rel, err := filepath.Rel(root, path)
	if err != nil {
		return false
	}
	return rel == "." || (rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)))
}

func (r *Roots) Dirs() []string {
	return append([]string(nil), r.dirs...)
}
