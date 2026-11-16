// kaudit-tui: group, chart and interactively explore Kubernetes audit logs.
package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
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
	)
	flag.Usage = func() {
		fmt.Fprintf(os.Stderr, "usage: %s [flags] audit.log [more.log.gz ...]   (use - for stdin)\n\n", filepath.Base(os.Args[0]))
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

	fmt.Fprintf(os.Stderr, "loading %s…\n", strings.Join(flag.Args(), ", "))
	res, err := audit.LoadFiles(flag.Args(), audit.LoadOptions{Stage: *stage})
	if err != nil {
		fail(err)
	}
	if len(res.Events) == 0 && res.Skipped > 0 {
		// log has no events of the requested stage; fall back to everything
		fmt.Fprintf(os.Stderr, "no %s events found, using all stages\n", *stage)
		res, err = audit.LoadFiles(flag.Args(), audit.LoadOptions{})
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
		return filepath.Base(paths[0])
	}
	return fmt.Sprintf("%d files", len(paths))
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
