package session

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"net/url"
	"regexp"
	"strings"
)

var ErrBadSession = errors.New("session name must match ^[A-Za-z0-9_-]{1,40}$")

var sessionName = regexp.MustCompile(`^[A-Za-z0-9_-]{1,40}$`)

func ValidName(name string) bool {
	return sessionName.MatchString(name)
}

func NameForDir(dir string) string {
	sum := sha256.Sum256([]byte(dir))
	return "tg-" + hex.EncodeToString(sum[:])[:12]
}

func Target(q url.Values, roots *Roots, home string) (name, dir string, err error) {
	rawDir := strings.TrimSpace(q.Get("dir"))
	rawName := strings.TrimSpace(q.Get("session"))
	if rawName != "" && !ValidName(rawName) {
		return "", "", ErrBadSession
	}
	if rawDir != "" {
		resolved, err := roots.Resolve(rawDir)
		if err != nil {
			return "", "", err
		}
		if rawName == "" {
			rawName = NameForDir(resolved)
		}
		return rawName, resolved, nil
	}
	if rawName != "" {
		resolved, err := roots.Resolve(home)
		if err != nil {
			return "", "", err
		}
		return rawName, resolved, nil
	}
	resolved, err := roots.Resolve(home)
	if err != nil {
		return "", "", err
	}
	return NameForDir(resolved), resolved, nil
}
