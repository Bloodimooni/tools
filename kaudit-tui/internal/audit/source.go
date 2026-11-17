package audit

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/url"
	"os"
	"os/exec"
	"strings"
	"syscall"
)

// SourceOptions control how a source is opened.
type SourceOptions struct {
	Follow  bool // keep reading as the log grows (tail -F)
	Backlog int  // follow mode: lines of existing log to read first; <0 = whole file
	Sudo    bool // ssh sources: run the remote command via `sudo -n`
}

// Source is an open log stream. Close stops it (and kills any child process).
type Source struct {
	io.Reader
	Name   string
	cancel func()
	wait   func() error
	stderr *bytes.Buffer
}

// Wait waits for a source that reached EOF to finish and returns its exit error.
func (s *Source) Wait() error { return s.wait() }

// Close stops the source, killing its child process if it has one.
func (s *Source) Close() error {
	s.cancel()
	return s.wait()
}

// Err returns the last stderr line of a child process, if any. Useful to
// explain why an ssh source ended (auth failure, permission denied, ...).
func (s *Source) Err() string {
	if s.stderr == nil {
		return ""
	}
	lines := strings.Split(strings.TrimSpace(s.stderr.String()), "\n")
	return lines[len(lines)-1]
}

// OpenSource opens "-" (stdin), a local path, or ssh://[user@]host[:port]/path.
func OpenSource(ctx context.Context, spec string, opt SourceOptions) (*Source, error) {
	if spec == "-" {
		return &Source{Reader: os.Stdin, Name: "stdin", cancel: func() {}, wait: func() error { return nil }}, nil
	}
	if strings.HasPrefix(spec, "ssh://") {
		return openSSH(ctx, spec, opt)
	}
	if opt.Follow {
		return run(ctx, spec, "tail", tailArgs(opt, spec)...)
	}
	f, err := os.Open(spec)
	if err != nil {
		return nil, err
	}
	return &Source{Reader: f, Name: spec, cancel: func() {}, wait: f.Close}, nil
}

func tailArgs(opt SourceOptions, path string) []string {
	n := "+1"
	if opt.Backlog >= 0 {
		n = fmt.Sprint(opt.Backlog)
	}
	return []string{"-n", n, "-F", path}
}

func openSSH(ctx context.Context, spec string, opt SourceOptions) (*Source, error) {
	u, err := url.Parse(spec)
	if err != nil {
		return nil, err
	}
	if u.Host == "" || u.Path == "" || u.Path == "/" {
		return nil, fmt.Errorf("want ssh://[user@]host[:port]/path/to/audit.log")
	}
	var remote []string
	if opt.Sudo {
		remote = append(remote, "sudo", "-n")
	}
	if opt.Follow {
		remote = append(remote, "tail")
		remote = append(remote, tailArgs(opt, u.Path)...)
	} else {
		remote = append(remote, "cat", u.Path)
	}
	for i, a := range remote {
		remote[i] = shellQuote(a)
	}
	// BatchMode: never prompt for a password, it would fight the TUI for the terminal.
	args := []string{"-o", "BatchMode=yes", "-o", "ServerAliveInterval=15"}
	if p := u.Port(); p != "" {
		args = append(args, "-p", p)
	}
	host := u.Hostname()
	if u.User != nil {
		host = u.User.Username() + "@" + host
	}
	args = append(args, host, "--", strings.Join(remote, " "))
	src, err := run(ctx, spec, "ssh", args...)
	if src != nil {
		src.Name = u.Hostname()
	}
	return src, err
}

func run(ctx context.Context, name, cmd string, args ...string) (*Source, error) {
	ctx, cancel := context.WithCancel(ctx)
	c := exec.CommandContext(ctx, cmd, args...)
	// Own process group: terminal signals don't reach the child, and on
	// cancel we kill the whole group, including anything the child spawned.
	c.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	c.Cancel = func() error { return syscall.Kill(-c.Process.Pid, syscall.SIGKILL) }
	out, err := c.StdoutPipe()
	if err != nil {
		cancel()
		return nil, err
	}
	stderr := &bytes.Buffer{}
	c.Stderr = stderr // never let a child write over the TUI
	if err := c.Start(); err != nil {
		cancel()
		return nil, err
	}
	return &Source{Reader: out, Name: name, stderr: stderr, cancel: cancel, wait: func() error {
		defer cancel()
		return c.Wait()
	}}, nil
}

func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}
