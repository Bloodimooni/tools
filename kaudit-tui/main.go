// kaudit-tui: group, chart and interactively explore Kubernetes audit logs.
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"golang.org/x/term"

	"kaudit-tui/internal/audit"
	"kaudit-tui/internal/hist"
	"kaudit-tui/internal/ui"
)

func main() {
	var (
		summary = flag.Bool("summary", false, "print histogram and top groups to stdout instead of starting the TUI")
		top     = flag.Int("top", 25, "number of groups in --summary")
		groupBy = flag.String("group-by", "similar", "similar|user|resource|verb|namespace|sourceIP|userAgent|status|none")
		stage   = flag.String("stage", "ResponseComplete", "only keep events of this stage (empty = all stages)")
		filterS = flag.String("filter", "", "initial filter expression, e.g. 'code:>=400 -verb:watch'")
		width   = flag.Int("width", 0, "chart width for --summary (default: terminal width or 100)")
		follow  = flag.Bool("follow", false, "stream new events live (tail -F) from every source")
		backlog = flag.Int("backlog", 10000, "with --follow: lines of existing log to read first per source (-1 = whole file)")
		sudo    = flag.Bool("sudo", false, "ssh:// sources: read the remote log via `sudo -n`")
	)
	flag.BoolVar(follow, "f", false, "shorthand for --follow")
	flag.Usage = func() {
		fmt.Fprintf(os.Stderr, "usage: %s [flags] SOURCE...\n\nSOURCE is a local file (.gz ok), - for stdin, or ssh://[user@]host[:port]/path\n\n", filepath.Base(os.Args[0]))
		flag.PrintDefaults()
	}
	flag.Parse()
	if flag.NArg() == 0 {
		flag.Usage()
		os.Exit(2)
	}
	by, err := audit.ParseGroupBy(*groupBy)
	if err != nil {
		fail(err)
	}
	filter, err := audit.ParseFilter(*filterS)
	if err != nil {
		fail(err)
	}

	if *follow {
		if *summary {
			fail(fmt.Errorf("--summary and --follow cannot be combined"))
		}
		runFollow(flag.Args(), by, filter, *filterS, audit.LoadOptions{Stage: *stage},
			audit.SourceOptions{Follow: true, Backlog: *backlog, Sudo: *sudo})
		return
	}

	fmt.Fprintf(os.Stderr, "loading %s…\n", strings.Join(flag.Args(), ", "))
	res, err := audit.LoadFiles(flag.Args(), audit.LoadOptions{Stage: *stage, Sudo: *sudo})
	if err != nil {
		fail(err)
	}
	if len(res.Events) == 0 && res.Skipped > 0 {
		// log has no events of the requested stage; fall back to everything
		fmt.Fprintf(os.Stderr, "no %s events found, using all stages\n", *stage)
		res, err = audit.LoadFiles(flag.Args(), audit.LoadOptions{Sudo: *sudo})
		if err != nil {
			fail(err)
		}
	}
	if len(res.Events) == 0 {
		fail(fmt.Errorf("no audit events found (%d unparsable lines)", res.BadLine))
	}
	header := describeInputs(flag.Args())
	if res.BadLine > 0 {
		header += fmt.Sprintf(" · %d bad lines", res.BadLine)
	}
	if res.Skipped > 0 {
		header += " · stage=" + *stage
	}

	if *summary {
		printSummary(res.Events, by, filter, *top, *width)
		return
	}

	m := ui.New(res.Events, by, header)
	if !filter.Empty() {
		m = m.WithFilter(*filterS, filter)
	}
	if _, err := tea.NewProgram(m, tea.WithAltScreen()).Run(); err != nil {
		fail(err)
	}
}

func describeInputs(paths []string) string {
	if len(paths) == 1 {
		if strings.HasPrefix(paths[0], "ssh://") {
			return paths[0]
		}
		return filepath.Base(paths[0])
	}
	return fmt.Sprintf("%d sources", len(paths))
}

// runFollow starts the TUI empty and streams events from every source into it.
func runFollow(specs []string, by audit.GroupBy, filter *audit.Filter, filterS string, lopt audit.LoadOptions, sopt audit.SourceOptions) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var srcs []*audit.Source
	for _, spec := range specs {
		src, err := audit.OpenSource(ctx, spec, sopt)
		if err != nil {
			cancel()
			fail(fmt.Errorf("%s: %w", spec, err))
		}
		srcs = append(srcs, src)
	}

	header := describeInputs(specs)
	if lopt.Stage != "" {
		header += " · stage=" + lopt.Stage
	}
	m := ui.New(nil, by, header).WithLive(len(srcs))
	if !filter.Empty() {
		m = m.WithFilter(filterS, filter)
	}
	p := tea.NewProgram(m, tea.WithAltScreen())

	for _, src := range srcs {
		go streamSource(ctx, p, src, lopt)
	}
	_, err := p.Run()
	cancel()
	for _, src := range srcs {
		src.Close()
	}
	if err != nil {
		fail(err)
	}
}

// streamSource parses one source and delivers events to the UI in batches,
// so a burst of thousands of lines causes a few re-renders, not thousands.
func streamSource(ctx context.Context, p *tea.Program, src *audit.Source, opt audit.LoadOptions) {
	var (
		mu    sync.Mutex
		batch []audit.Event
	)
	flush := func() {
		mu.Lock()
		b := batch
		batch = nil
		mu.Unlock()
		if len(b) > 0 {
			p.Send(ui.EventsMsg(b))
		}
	}
	done := make(chan struct{})
	go func() {
		t := time.NewTicker(250 * time.Millisecond)
		defer t.Stop()
		for {
			select {
			case <-t.C:
				flush()
			case <-done:
				return
			}
		}
	}()

	var res audit.LoadResult
	err := audit.Scan(src, opt, &res, func(e audit.Event) {
		mu.Lock()
		batch = append(batch, e)
		mu.Unlock()
	})
	close(done)
	flush()
	if ctx.Err() != nil {
		return // we are shutting down
	}
	src.Wait() // reap the child so its stderr is complete
	msg := src.Err()
	if msg == "" && err != nil {
		msg = err.Error()
	}
	p.Send(ui.SourceDoneMsg{Name: src.Name, Err: msg})
}

func printSummary(events []audit.Event, by audit.GroupBy, f *audit.Filter, top, width int) {
	if width <= 0 {
		width = 100
		if w, _, err := term.GetSize(int(os.Stdout.Fd())); err == nil && w > 20 {
			width = w
		}
	}
	var evs []*audit.Event
	for i := range events {
		if f.Match(&events[i]) {
			evs = append(evs, &events[i])
		}
	}
	if len(evs) == 0 {
		fmt.Println("no events match")
		return
	}
	times := make([]time.Time, len(evs))
	for i, e := range evs {
		times[i] = e.Time
	}
	from, to := evs[0].Time, evs[len(evs)-1].Time
	h := hist.Build(times, from, to, width-10)
	p := h.Peak()
	fmt.Printf("%d events  %s – %s\n", len(evs), from.Format("2006-01-02 15:04:05"), to.Format("2006-01-02 15:04:05"))
	fmt.Printf("requests per %s · peak %d at %s\n\n", hist.FormatStep(h.Step), h.Counts[p], h.BucketTime(p).Format("2006-01-02 15:04:05"))
	fmt.Println(h.Render(hist.RenderOpts{Height: 8, Cursor: -1, SelFrom: -1, SelTo: -1}))
	fmt.Println()

	groups := audit.BuildGroups(evs, by, audit.SortCount)
	fmt.Printf("%d groups (by %s), top %d:\n", len(groups), by, min(top, len(groups)))
	fmt.Printf("%8s %6s  %s\n", "COUNT", "ERR", "GROUP")
	for i, g := range groups {
		if i >= top {
			break
		}
		codes := make([]string, 0, len(g.Codes))
		for c, n := range g.Codes {
			codes = append(codes, fmt.Sprintf("%d×%d", c, n))
		}
		sort.Strings(codes)
		errS := ""
		if g.Errors > 0 {
			errS = fmt.Sprint(g.Errors)
		}
		fmt.Printf("%8d %6s  %s  [%s]\n", len(g.Events), errS, g.Title(), strings.Join(codes, " "))
	}
}

func fail(err error) {
	fmt.Fprintln(os.Stderr, "error:", err)
	os.Exit(1)
}
