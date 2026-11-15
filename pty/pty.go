package pty

import (
	"os"
	"os/exec"
	"syscall"

	creackpty "github.com/creack/pty"
)

type Proc struct {
	Cmd *exec.Cmd
	F   *os.File
}

func Spawn(dir string, argv []string, env []string, rows, cols uint16) (*Proc, error) {
	cmd := exec.Command(argv[0], argv[1:]...)
	cmd.Dir = dir
	cmd.Env = env
	f, err := creackpty.StartWithSize(cmd, &creackpty.Winsize{Rows: rows, Cols: cols})
	if err != nil {
		return nil, err
	}
	return &Proc{Cmd: cmd, F: f}, nil
}

func (p *Proc) Read(b []byte) (int, error) {
	return p.F.Read(b)
}

func (p *Proc) Write(b []byte) (int, error) {
	return p.F.Write(b)
}

func (p *Proc) Resize(rows, cols uint16) error {
	return creackpty.Setsize(p.F, &creackpty.Winsize{Rows: rows, Cols: cols})
}

func (p *Proc) Kill() {
	if p.Cmd.Process != nil && p.Cmd.ProcessState == nil {
		if err := syscall.Kill(-p.Cmd.Process.Pid, syscall.SIGKILL); err != nil {
			_ = p.Cmd.Process.Kill()
		}
	}
	_ = p.F.Close()
}

func (p *Proc) Wait() error {
	return p.Cmd.Wait()
}
