package audit

import (
	"bufio"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// fakeSSH puts an `ssh` on PATH that records its args and runs the remote
// command locally, so the real ssh:// code path runs without a server.
func fakeSSH(t *testing.T) (argsFile string) {
	dir := t.TempDir()
	argsFile = filepath.Join(dir, "args")
	script := "#!/bin/sh\necho \"$@\" > " + argsFile + "\nfor a; do last=$a; done\nexec sh -c \"$last\"\n"
	if err := os.WriteFile(filepath.Join(dir, "ssh"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	// and a sudo that just runs the command (drops -n)
	os.WriteFile(filepath.Join(dir, "sudo"), []byte("#!/bin/sh\nshift\nexec \"$@\"\n"), 0o755)
	t.Setenv("PATH", dir+":"+os.Getenv("PATH"))
	return argsFile
}

func TestLoadSSH(t *testing.T) {
	argsFile := fakeSSH(t)
	log, _ := filepath.Abs("../../testdata/sample.jsonl")
	res, err := LoadFiles([]string{"ssh://admin@m1:2222" + log}, LoadOptions{Sudo: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Events) == 0 {
		t.Fatal("no events")
	}
	args, _ := os.ReadFile(argsFile)
	want := "-p 2222 admin@m1 -- 'sudo' '-n' 'cat' '" + log + "'"
	if !strings.Contains(string(args), want) {
		t.Fatalf("ssh args %q, want %q", args, want)
	}
}

func TestFollowSSH(t *testing.T) {
	fakeSSH(t)
	log := filepath.Join(t.TempDir(), "audit.log")
	line := `{"kind":"Event","stage":"ResponseComplete","stageTimestamp":"2026-01-01T00:00:00Z","verb":"get"}` + "\n"
	os.WriteFile(log, []byte(line+line), 0o644)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	src, err := OpenSource(ctx, "ssh://m1"+log, SourceOptions{Follow: true, Backlog: 1})
	if err != nil {
		t.Fatal(err)
	}
	defer src.Close()
	sc := bufio.NewScanner(src)
	read := func() {
		if !sc.Scan() {
			t.Fatalf("stream ended: %v %s", sc.Err(), src.Err())
		}
	}
	read() // backlog of 1 line
	f, _ := os.OpenFile(log, os.O_APPEND|os.O_WRONLY, 0)
	f.WriteString(line)
	f.Close()
	read() // the appended line arrives live
}
