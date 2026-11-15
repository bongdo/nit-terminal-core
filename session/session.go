package session

import (
	"errors"
	"io"
	"log"
	"os"
	"os/exec"
	"sort"
	"strings"
	"sync"

	"termcore/pty"
)

var (
	ErrTooMany     = errors.New("too many sessions; close another terminal tab")
	ErrDirMismatch = errors.New("session name is already used for another directory")
)

const tmuxConfig = `set-option -g destroy-unattached off
set-option -g allow-passthrough on
set-option -g status off
set-option -g history-limit 10000
set-option -g escape-time 0
set-option -g set-clipboard on
set-option -g mouse on
set-environment -g COLORTERM truecolor
bind-key -T copy-mode MouseDragEnd1Pane send-keys -X copy-selection-and-cancel
bind-key -T copy-mode-vi MouseDragEnd1Pane send-keys -X copy-selection-and-cancel
bind-key -n F11 copy-mode -u
bind-key -n F12 copy-mode -e
bind-key -T copy-mode F11 send-keys -X page-up
bind-key -T copy-mode F12 send-keys -X page-down
bind-key -T copy-mode-vi F11 send-keys -X page-up
bind-key -T copy-mode-vi F12 send-keys -X page-down
`

type Client struct {
	C    chan []byte
	Dead chan struct{}
	once sync.Once
}

func (c *Client) kill() {
	c.once.Do(func() { close(c.Dead) })
}

type Info struct {
	Name     string `json:"name"`
	Dir      string `json:"dir"`
	Tmux     bool   `json:"tmux"`
	Attached int    `json:"attached"`
}

type Manager struct {
	mu         sync.Mutex
	home       string
	max        int
	tmux       bool
	tmuxConfig string
	shell      func() []string
	sessions   map[string]*Session
}

func New(home string, max int) *Manager {
	if max < 1 {
		max = 1
	}
	config, err := writeTmuxConfig()
	if err != nil {
		log.Printf("tmux config: %v", err)
		config = ""
	}
	_, err = exec.LookPath("tmux")
	return &Manager{
		home:       home,
		max:        max,
		tmux:       err == nil && config != "",
		tmuxConfig: config,
		shell:      defaultShell,
		sessions:   map[string]*Session{},
	}
}

func writeTmuxConfig() (string, error) {
	file, err := os.CreateTemp("", "term-gui-tmux-*.conf")
	if err != nil {
		return "", err
	}
	name := file.Name()
	if _, err := file.WriteString(tmuxConfig); err != nil {
		_ = file.Close()
		_ = os.Remove(name)
		return "", err
	}
	if err := file.Close(); err != nil {
		_ = os.Remove(name)
		return "", err
	}
	return name, nil
}

func defaultShell() []string {
	shell := strings.TrimSpace(os.Getenv("SHELL"))
	if shell == "" {
		shell = "/bin/sh"
	}
	return greet([]string{shell})
}

const greetCommand = `command -v fastfetch >/dev/null 2>&1 && fastfetch; exec "$0" "$@"`

func greet(argv []string) []string {
	if len(argv) == 0 {
		return argv
	}
	return append([]string{"/bin/sh", "-c", greetCommand, argv[0]}, argv[1:]...)
}

func quoteArgs(argv []string) string {
	quoted := make([]string, len(argv))
	for index, arg := range argv {
		quoted[index] = "'" + strings.ReplaceAll(arg, "'", `'\''`) + "'"
	}
	return strings.Join(quoted, " ")
}

func (m *Manager) Home() string { return m.home }

func (m *Manager) Tmux() bool { return m.tmux }

func (m *Manager) Validate(name, dir string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if s, ok := m.sessions[name]; ok {
		if s.Dir != dir {
			return ErrDirMismatch
		}
		return nil
	}
	if len(m.sessions) >= m.max {
		return ErrTooMany
	}
	return nil
}

func (m *Manager) Attach(name, dir string, rows, cols uint16) (*Session, *Client, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if s, ok := m.sessions[name]; ok {
		if s.Dir != dir {
			return nil, nil, ErrDirMismatch
		}
		return s, s.subscribe(), nil
	}
	if len(m.sessions) >= m.max {
		return nil, nil, ErrTooMany
	}
	s, err := start(name, dir, m.tmux, m.tmuxConfig, m.shell, rows, cols)
	if err != nil {
		return nil, nil, err
	}
	client := s.subscribe()
	m.sessions[name] = s
	go s.pump(func() { m.forget(s) })
	return s, client, nil
}

func (m *Manager) Detach(s *Session, c *Client) {
	m.mu.Lock()
	s.mu.Lock()
	delete(s.clients, c)
	empty := len(s.clients) == 0
	if empty && m.sessions[s.Name] == s {
		delete(m.sessions, s.Name)
	}
	s.mu.Unlock()
	s.SetPaused(c, false)
	c.kill()
	if empty {
		s.Close()
	}
	m.mu.Unlock()
}

func (m *Manager) forget(s *Session) {
	m.mu.Lock()
	if m.sessions[s.Name] == s {
		delete(m.sessions, s.Name)
	}
	m.mu.Unlock()
}

func (m *Manager) CloseAll() {
	m.mu.Lock()
	sessions := make([]*Session, 0, len(m.sessions))
	for _, s := range m.sessions {
		sessions = append(sessions, s)
	}
	m.sessions = map[string]*Session{}
	m.mu.Unlock()
	for _, s := range sessions {
		s.Close()
	}
}

func (m *Manager) List() []Info {
	m.mu.Lock()
	infos := make([]Info, 0, len(m.sessions))
	seen := make(map[string]bool, len(m.sessions))
	for name, s := range m.sessions {
		infos = append(infos, Info{Name: name, Dir: s.Dir, Tmux: s.Tmux, Attached: s.clientCount()})
		seen[name] = true
	}
	tmux := m.tmux
	m.mu.Unlock()
	if tmux {
		for _, info := range tmuxSessions() {
			if !seen[info.Name] {
				infos = append(infos, info)
			}
		}
	}
	sort.Slice(infos, func(i, j int) bool { return infos[i].Name < infos[j].Name })
	return infos
}

func tmuxCommand(conf string, args ...string) *exec.Cmd {
	if conf == "" {
		conf = "/dev/null"
	}
	cmd := exec.Command("tmux", append([]string{"-L", "term-gui", "-f", conf}, args...)...)
	cmd.Env = sessionEnv()
	return cmd
}

func tmuxSessions() []Info {
	out, err := tmuxCommand("", "list-sessions", "-F", "#{session_name}\t#{session_path}").Output()
	if err != nil {
		return nil
	}
	var infos []Info
	for _, line := range strings.Split(string(out), "\n") {
		parts := strings.SplitN(line, "\t", 2)
		if len(parts) != 2 || parts[0] == "" {
			continue
		}
		infos = append(infos, Info{Name: parts[0], Dir: parts[1], Tmux: true})
	}
	return infos
}

func start(name, dir string, tmux bool, conf string, shell func() []string, rows, cols uint16) (*Session, error) {
	plain := shell()
	argv := plain
	if tmux {
		if tmuxRunning(conf) {
			if err := tmuxCommand(conf, "source-file", conf).Run(); err != nil {
				log.Printf("tmux source-file: %v", err)
				tmux = false
			}
		}
		if tmux {
			_ = tmuxCommand(conf, "set-option", "-g", "destroy-unattached", "off").Run()
			tmuxScrubEnv(conf)
			if err := tmuxCommand(conf, "has-session", "-t", "="+name).Run(); err != nil {
				if out, err := tmuxCommand(conf, "new-session", "-d", "-s", name, "-c", dir, quoteArgs(plain)).CombinedOutput(); err != nil {
					log.Printf("tmux new-session: %v: %s", err, strings.TrimSpace(string(out)))
					tmux = false
				}
			}
		}
		if tmux {
			argv = []string{"tmux", "-L", "term-gui", "-f", conf, "attach-session", "-t", "=" + name}
		}
	}
	env := sessionEnv()
	env = setEnv(env, "TERM", "xterm-256color")
	env = setEnv(env, "COLORTERM", "truecolor")
	proc, err := pty.Spawn(dir, argv, env, rows, cols)
	if err != nil && tmux {
		_ = tmuxCommand(conf, "kill-session", "-t", "="+name).Run()
		tmux = false
		argv = plain
		proc, err = pty.Spawn(dir, argv, env, rows, cols)
	}
	if err != nil {
		return nil, err
	}
	if tmux {
		if err := tmuxCommand(conf, "set-option", "-g", "destroy-unattached", "on").Run(); err != nil {
			log.Printf("tmux destroy-unattached: %v", err)
		}
	}
	s := &Session{
		Name:    name,
		Dir:     dir,
		Tmux:    tmux,
		proc:    proc,
		clients: map[*Client]struct{}{},
		paused:  map[*Client]struct{}{},
		in:      make(chan []byte, 256),
		done:    make(chan struct{}),
	}
	s.flowCond = sync.NewCond(&s.flowMu)
	go s.writeLoop()
	return s, nil
}

func tmuxRunning(conf string) bool {
	return tmuxCommand(conf, "list-sessions").Run() == nil
}

type Session struct {
	Name string
	Dir  string
	Tmux bool

	proc *pty.Proc

	mu      sync.Mutex
	clients map[*Client]struct{}
	closed  bool

	flowMu     sync.Mutex
	flowCond   *sync.Cond
	paused     map[*Client]struct{}
	flowClosed bool

	in   chan []byte
	done chan struct{}
}

func (s *Session) Done() <-chan struct{} { return s.done }

func (s *Session) subscribe() *Client {
	c := &Client{C: make(chan []byte, 64), Dead: make(chan struct{})}
	s.mu.Lock()
	s.clients[c] = struct{}{}
	s.mu.Unlock()
	return c
}

func (s *Session) clientCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.clients)
}

func (s *Session) Write(p []byte) error {
	data := append([]byte(nil), p...)
	select {
	case s.in <- data:
		return nil
	case <-s.done:
		return io.ErrClosedPipe
	}
}

func (s *Session) writeLoop() {
	for {
		select {
		case data := <-s.in:
			if _, err := s.proc.Write(data); err != nil {
				return
			}
		case <-s.done:
			return
		}
	}
}

func (s *Session) Resize(rows, cols uint16) error {
	return s.proc.Resize(rows, cols)
}

func (s *Session) SetPaused(c *Client, paused bool) {
	s.flowMu.Lock()
	if paused {
		s.paused[c] = struct{}{}
	} else {
		delete(s.paused, c)
	}
	s.flowCond.Broadcast()
	s.flowMu.Unlock()
}

func (s *Session) waitResumed() {
	s.flowMu.Lock()
	for len(s.paused) > 0 && !s.flowClosed {
		s.flowCond.Wait()
	}
	s.flowMu.Unlock()
}

func (s *Session) closeFlow() {
	s.flowMu.Lock()
	s.flowClosed = true
	s.flowCond.Broadcast()
	s.flowMu.Unlock()
}

func (s *Session) Close() {
	s.mu.Lock()
	already := s.closed
	s.closed = true
	clients := make([]*Client, 0, len(s.clients))
	for c := range s.clients {
		clients = append(clients, c)
	}
	s.mu.Unlock()
	s.closeFlow()
	for _, c := range clients {
		c.kill()
	}
	if s.Tmux {
		_ = tmuxCommand("", "kill-session", "-t", "="+s.Name).Run()
	}
	if !already {
		s.proc.Kill()
	}
}

func (s *Session) pump(done func()) {
	buf := make([]byte, 16*1024)
	for {
		s.waitResumed()
		n, err := s.proc.Read(buf)
		if n > 0 {
			data := make([]byte, n)
			copy(data, buf[:n])
			s.broadcast(data)
		}
		if err != nil {
			break
		}
	}
	s.proc.Kill()
	_ = s.proc.Wait()
	s.mu.Lock()
	s.closed = true
	s.mu.Unlock()
	s.closeFlow()
	close(s.done)
	done()
}

func (s *Session) broadcast(data []byte) {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return
	}
	clients := make([]*Client, 0, len(s.clients))
	for c := range s.clients {
		clients = append(clients, c)
	}
	s.mu.Unlock()
	for _, c := range clients {
		select {
		case c.C <- data:
		case <-c.Dead:
		case <-s.done:
			return
		}
	}
}

func sessionEnv() []string {
	var out []string
	for _, item := range os.Environ() {
		key, _, _ := strings.Cut(item, "=")
		if key == "TMUX" || strings.HasPrefix(key, "NIT_") || strings.HasPrefix(key, "OPENCODE_") {
			continue
		}
		out = append(out, item)
	}
	return out
}

func tmuxScrubEnv(conf string) {
	for _, item := range os.Environ() {
		key, _, _ := strings.Cut(item, "=")
		if strings.HasPrefix(key, "NIT_") || strings.HasPrefix(key, "OPENCODE_") {
			_ = tmuxCommand(conf, "set-environment", "-g", "-u", key).Run()
		}
	}
}

func setEnv(env []string, key, value string) []string {
	out := make([]string, 0, len(env)+1)
	for _, item := range env {
		if strings.HasPrefix(item, key+"=") {
			continue
		}
		out = append(out, item)
	}
	return append(out, key+"="+value)
}
