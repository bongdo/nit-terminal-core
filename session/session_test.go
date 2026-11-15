package session

import (
	"bytes"
	"errors"
	"net/url"
	"os/exec"
	"strings"
	"testing"
	"time"
)

func TestGreetKeepsShell(t *testing.T) {
	argv := greet([]string{"/bin/sh", "-c", "printf ok"})
	out, err := exec.Command(argv[0], argv[1:]...).Output()
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasSuffix(string(out), "ok") {
		t.Fatalf("output = %q, want it to end with the shell output", out)
	}
}

func TestQuoteArgsSurviveSh(t *testing.T) {
	argv := []string{"/bin/sh", "-c", `printf '%s|%s' "a b" 'c'`, "extra"}
	out, err := exec.Command("/bin/sh", "-c", quoteArgs(argv)).Output()
	if err != nil {
		t.Fatal(err)
	}
	if string(out) != "a b|c" {
		t.Fatalf("output = %q, want %q", out, "a b|c")
	}
}

func TestPtyOracle(t *testing.T) {
	dir := t.TempDir()
	manager := New(dir, 2)
	manager.tmux = false
	manager.shell = func() []string { return []string{"/bin/sh", "-c", "printf hello"} }

	sess, client, err := manager.Attach("oracle", dir, 24, 80)
	if err != nil {
		t.Fatal(err)
	}
	defer manager.Detach(sess, client)

	var out bytes.Buffer
	deadline := time.After(5 * time.Second)
	for !bytes.Contains(out.Bytes(), []byte("hello")) {
		select {
		case data := <-client.C:
			out.Write(data)
		case <-deadline:
			t.Fatalf("no output, got %q", out.String())
		}
	}
}

func TestSessionEnvScrubsSecrets(t *testing.T) {
	t.Setenv("NIT_GATE_PASSWORD_HASH", "secret")
	t.Setenv("NIT_GUI_PASSWORD", "secret")
	t.Setenv("OPENCODE_TOKEN", "secret")
	t.Setenv("TERM_GUI_TEST_VALUE", "kept")
	env := strings.Join(sessionEnv(), "\n")
	for _, secret := range []string{"NIT_GATE_PASSWORD_HASH", "NIT_GUI_PASSWORD", "OPENCODE_TOKEN"} {
		if strings.Contains(env, secret) {
			t.Fatalf("%s leaked into PTY environment", secret)
		}
	}
	if !strings.Contains(env, "TERM_GUI_TEST_VALUE=kept") {
		t.Fatal("unrelated environment was removed")
	}
}

func TestExplicitSessionHomeMustStayInsideRoots(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	roots, err := ParseRoots(root)
	if err != nil {
		t.Fatal(err)
	}
	values, err := url.ParseQuery("session=demo")
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := Target(values, roots, outside); !errors.Is(err, ErrOutside) {
		t.Fatalf("err = %v, want ErrOutside", err)
	}
	if _, _, err := Target(url.Values{}, roots, outside); !errors.Is(err, ErrOutside) {
		t.Fatalf("default err = %v, want ErrOutside", err)
	}
}

func TestPtyEnvScrubsSecrets(t *testing.T) {
	t.Setenv("NIT_GATE_PASSWORD_HASH", "secret")
	t.Setenv("OPENCODE_TOKEN", "secret")
	dir := t.TempDir()
	manager := New(dir, 1)
	manager.tmux = false
	manager.shell = func() []string { return []string{"/bin/sh", "-c", "env"} }

	sess, client, err := manager.Attach("env", dir, 24, 80)
	if err != nil {
		t.Fatal(err)
	}
	defer manager.Detach(sess, client)

	var out bytes.Buffer
	deadline := time.After(5 * time.Second)
	for {
		select {
		case data := <-client.C:
			out.Write(data)
		case <-sess.Done():
			if strings.Contains(out.String(), "NIT_GATE_PASSWORD_HASH") || strings.Contains(out.String(), "OPENCODE_TOKEN") {
				t.Fatalf("secret leaked into PTY: %q", out.String())
			}
			return
		case <-deadline:
			t.Fatalf("PTY did not finish: %q", out.String())
		}
	}
}

func TestAttachRejectsDifferentDirForSameName(t *testing.T) {
	first := t.TempDir()
	second := t.TempDir()
	manager := New(first, 1)
	manager.tmux = false
	manager.shell = func() []string { return []string{"/bin/sh", "-c", "sleep 5"} }

	sess, client, err := manager.Attach("same", first, 24, 80)
	if err != nil {
		t.Fatal(err)
	}
	defer manager.Detach(sess, client)

	if _, _, err := manager.Attach("same", second, 24, 80); !errors.Is(err, ErrDirMismatch) {
		t.Fatalf("err = %v, want ErrDirMismatch", err)
	}
}

func TestSlowClientDoesNotLoseSession(t *testing.T) {
	dir := t.TempDir()
	manager := New(dir, 1)
	manager.tmux = false
	manager.shell = func() []string { return []string{"/bin/sh", "-c", "seq 1 500000"} }

	sess, client, err := manager.Attach("slow", dir, 24, 80)
	if err != nil {
		t.Fatal(err)
	}
	defer manager.Detach(sess, client)

	time.Sleep(200 * time.Millisecond)
	if got := sess.clientCount(); got != 1 {
		t.Fatalf("client count = %d, want 1", got)
	}
	select {
	case <-sess.Done():
		t.Fatal("session ended before the client drained output")
	default:
	}

	deadline := time.After(15 * time.Second)
	for {
		select {
		case <-client.C:
		case <-sess.Done():
			return
		case <-deadline:
			t.Fatal("session did not finish after draining")
		}
	}
}

func TestPausedClientDoesNotRead(t *testing.T) {
	dir := t.TempDir()
	manager := New(dir, 1)
	manager.tmux = false
	manager.shell = func() []string { return []string{"/bin/sh", "-c", "sleep 0.2; printf hello"} }

	sess, client, err := manager.Attach("paused", dir, 24, 80)
	if err != nil {
		t.Fatal(err)
	}
	defer manager.Detach(sess, client)
	sess.SetPaused(client, true)

	select {
	case data := <-client.C:
		t.Fatalf("read %q while paused", data)
	case <-time.After(100 * time.Millisecond):
	}

	sess.SetPaused(client, false)
	select {
	case data := <-client.C:
		if !bytes.Contains(data, []byte("hello")) {
			t.Fatalf("output = %q", data)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("no output after resume")
	}
}

func TestCloseAllStopsSessions(t *testing.T) {
	dir := t.TempDir()
	manager := New(dir, 1)
	manager.tmux = false
	manager.shell = func() []string { return []string{"/bin/sh", "-c", "sleep 30"} }

	sess, client, err := manager.Attach("close", dir, 24, 80)
	if err != nil {
		t.Fatal(err)
	}
	defer manager.Detach(sess, client)
	manager.CloseAll()

	select {
	case <-sess.Done():
	case <-time.After(5 * time.Second):
		t.Fatal("session survived CloseAll")
	}
}

func TestPausedClientDisconnectDoesNotStallOthers(t *testing.T) {
	dir := t.TempDir()
	manager := New(dir, 1)
	manager.tmux = false
	manager.shell = func() []string { return []string{"/bin/sh", "-c", "sleep 0.2; printf hello"} }

	sess, first, err := manager.Attach("shared", dir, 24, 80)
	if err != nil {
		t.Fatal(err)
	}
	second := sess.subscribe()
	defer manager.Detach(sess, second)
	sess.SetPaused(first, true)
	manager.Detach(sess, first)

	deadline := time.After(5 * time.Second)
	for {
		select {
		case data := <-second.C:
			if bytes.Contains(data, []byte("hello")) {
				return
			}
		case <-deadline:
			t.Fatal("session stayed paused after the slow client left")
		}
	}
}
