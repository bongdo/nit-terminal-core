# termcore

Terminal engine behind term-gui, split into a local Go module so another binary
can host the same terminals without importing term-gui's `internal/` packages.

```go
require termcore v0.0.0

replace termcore => ../termcore
```

Go 1.26. Two dependencies: `github.com/creack/pty` and `github.com/coder/websocket`.

## Packages

### `termcore/pty`

A PTY process: spawn, read, write, resize, kill, wait.

```go
type Proc struct {
	Cmd *exec.Cmd
	F   *os.File
}

func Spawn(dir string, argv []string, env []string, rows, cols uint16) (*Proc, error)
func (p *Proc) Read(b []byte) (int, error)
func (p *Proc) Write(b []byte) (int, error)
func (p *Proc) Resize(rows, cols uint16) error
func (p *Proc) Kill()
func (p *Proc) Wait() error
```

`Kill` signals the whole process group and closes the PTY. `Spawn` runs `argv`
with `cmd.Dir = dir` and exactly the `env` given, so the caller decides what the
child may see.

### `termcore/session`

One session per workspace directory, backed by tmux when tmux is installed and a
plain shell otherwise.

```go
func New(home string, max int) *Manager
func ParseRoots(value string) (*Roots, error)

type Manager
func (m *Manager) Home() string
func (m *Manager) Tmux() bool
func (m *Manager) Validate(name, dir string) error
func (m *Manager) Attach(name, dir string, rows, cols uint16) (*Session, *Client, error)
func (m *Manager) Detach(s *Session, c *Client)
func (m *Manager) CloseAll()
func (m *Manager) List() []Info

type Session
func (s *Session) Done() <-chan struct{}
func (s *Session) Write(p []byte) error
func (s *Session) Resize(rows, cols uint16) error
func (s *Session) SetPaused(c *Client, paused bool)
func (s *Session) Close()

type Client struct { C chan []byte; Dead chan struct{} }
type Info struct { Name, Dir string; Tmux bool; Attached int }

type Roots
func (r *Roots) Resolve(dir string) (string, error)
func (r *Roots) Dirs() []string

func ValidName(name string) bool
func NameForDir(dir string) string
func Target(q url.Values, roots *Roots, home string) (name, dir string, err error)
```

- `New` starts no process. `Attach` starts one on first use and joins an existing
  session for the same name.
- `Validate` checks capacity and directory conflicts without starting anything; use
  it to reject a request before the WebSocket upgrade.
- `Client.C` receives output chunks, `Client.Dead` closes when the client is
  detached. `SetPaused` stops the PTY read pump, which is how a slow browser is
  kept from losing output.
- `List` merges live sessions with tmux sessions found on the socket, so terminals
  started by an earlier process still show up.
- Errors: `ErrTooMany`, `ErrDirMismatch`, `ErrBadSession`, and from `Roots`:
  `ErrNotAbsolute`, `ErrNotFound`, `ErrNotDir`, `ErrOutside`.

Sessions live in the tmux server `-L term-gui`, so any binary using this module
shares that socket and reattaches to the same tmux sessions.

### `termcore/ws`

The HTTP side: a WebSocket upgrade handler that bridges a browser to a session.

```go
type Handler struct {
	Roots         *session.Roots
	Manager       *session.Manager
	Home          string
	PeroURL       string
	DropURL       string
	OriginAllowed func(*http.Request) bool
	AllowNoOrigin bool
}

func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request)
func (h *Handler) Check(w http.ResponseWriter, r *http.Request)
```

- `ServeHTTP` answers a GET with a WebSocket: it sends a `hello` frame, then binary
  frames carry terminal bytes both ways. Text frames are control messages:
  `{"type":"resize","rows":R,"cols":C}` and `{"type":"flow","paused":true|false}`.
  Pings every 30s, 8 MiB frame limit, resize clamped to 1..1000.
- `Check` is the pre-flight endpoint: target validation plus `Manager.Validate`,
  answering `{"ok":true}`, 400, 409 or 429.
- The request target is taken from the query: `?dir=<absolute path>` and
  `?session=<name>`, resolved by `session.Target`.
- `PeroURL` and `DropURL` are only echoed back in the `hello` frame for the client.

## What consumers must provide

`termcore` has no server, no authentication, no TLS and no knowledge of any URL
prefix. The host binary is responsible for:

- a workspace root: `session.ParseRoots` with an absolute, existing directory
  (defaults to `$HOME` when the value is empty) and a `home` directory that lives
  inside it, since every default target resolves against `home`;
- an auth layer and access control in front of `/api/ws` and `/api/check` — the
  handler only checks `Origin` (via `OriginAllowed`, or same-host by default, and
  missing origin only when `AllowNoOrigin` is set);
- mounting the two handlers under whatever path and mux the host uses, and
  reporting health and session lists itself (`Manager.List`).
