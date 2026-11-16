// Package ui is the interactive Bubble Tea front end.
package ui

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/textinput"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"kaudit-tui/internal/audit"
	"kaudit-tui/internal/hist"
)

type view int

const (
	viewGroups view = iota
	viewEvents
	viewDetail
)

// Model is the Bubble Tea model.
type Model struct {
	all      []*audit.Event
	minT     time.Time
	maxT     time.Time
	winFrom  time.Time // zoom window (zero = full range)
	winTo    time.Time
	filter   *audit.Filter
	fText    string
	groupBy  int // index into audit.GroupPresets
	sortBy   audit.SortMode
	filtered []*audit.Event
	groups   []*audit.Group

	view    view
	gCur    int
	gOff    int
	group   *audit.Group // group open in events view
	eCur    int
	eOff    int
	eSort   int
	evList  []*audit.Event // group events in the current event sort order
	detail  *audit.Event
	vp      viewport.Model
	marked  map[*audit.Event]bool
	histSel bool // groups view: histogram shows only the selected group

	timeMode bool // histogram cursor active
	hCur     int
	hSel     int // range start bucket, -1 none
	h        *hist.Hist

	input    textinput.Model
	inputOn  bool
	cmdOn    bool // input line is a ":" command, not a filter
	showHelp bool
	status   string
	width    int
	height   int
	header   string
}

// New builds a model over the given events (must be sorted by time).
func New(events []audit.Event, by audit.GroupBy, header string) Model {
	m := Model{marked: map[*audit.Event]bool{}, hSel: -1, header: header}
	m.all = make([]*audit.Event, len(events))
	for i := range events {
		m.all[i] = &events[i]
	}
	if len(m.all) > 0 {
		m.minT, m.maxT = m.all[0].Time, m.all[len(m.all)-1].Time
	}
	for i, g := range audit.GroupPresets {
		if g == by {
			m.groupBy = i
		}
	}
	ti := textinput.New()
	ti.Prompt = "/ "
	ti.Placeholder = filterPlaceholder
	ti.CharLimit = 500
	m.input = ti
	m.vp = viewport.New(80, 20)
	m.refresh()
	return m
}

// WithFilter applies an initial filter.
func (m Model) WithFilter(text string, f *audit.Filter) Model {
	m.filter, m.fText = f, text
	m.refresh()
	return m
}

func (m Model) Init() tea.Cmd { return nil }

func (m *Model) window() (time.Time, time.Time) {
	if !m.winFrom.IsZero() {
		return m.winFrom, m.winTo
	}
	return m.minT, m.maxT
}

func (m *Model) zoomed() bool { return !m.winFrom.IsZero() }

// refresh recomputes the filtered events and groups, keeping the user's place.
func (m *Model) refresh() {
	from, to := m.window()
	m.filtered = m.filtered[:0]
	for _, e := range m.all {
		if m.zoomed() && (e.Time.Before(from) || e.Time.After(to)) {
			continue
		}
		if m.filter.Match(e) {
			m.filtered = append(m.filtered, e)
		}
	}
	var curKey string
	if m.gCur < len(m.groups) {
		curKey = m.groups[m.gCur].Key
	}
	m.groups = audit.BuildGroups(m.filtered, audit.GroupPresets[m.groupBy], m.sortBy)
	m.gCur = 0
	for i, g := range m.groups {
		if g.Key == curKey {
			m.gCur = i
			break
		}
	}
	if m.group != nil {
		var found *audit.Group
		for _, g := range m.groups {
			if g.Key == m.group.Key {
				found = g
			}
		}
		m.group = found
		if found == nil && m.view != viewGroups {
			m.view = viewGroups
			m.status = "group no longer matches the filter"
		}
		if m.group != nil {
			m.sortEvents()
			m.eCur = max(0, min(m.eCur, len(m.evList)-1))
		}
	}
}

// histEvents is the event set the histogram currently shows.
func (m *Model) histEvents() ([]*audit.Event, string) {
	switch {
	case m.view == viewEvents && m.group != nil:
		return m.group.Events, "group"
	case m.view == viewGroups && m.histSel && m.gCur < len(m.groups):
		return m.groups[m.gCur].Events, "selected group"
	}
	return m.filtered, "all matching"
}

func (m *Model) buildHist() {
	evs, _ := m.histEvents()
	ts := make([]time.Time, len(evs))
	for i, e := range evs {
		ts[i] = e.Time
	}
	from, to := m.window()
	m.h = hist.Build(ts, from, to, max(10, m.width-9))
	m.hCur = min(max(m.hCur, 0), len(m.h.Counts)-1)
}

func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		m.vp.Width, m.vp.Height = msg.Width, max(3, msg.Height-3)
		m.input.Width = msg.Width - 4
		m.buildHist()
		m.ensureVisible()
		return m, nil
	case tea.KeyMsg:
		if m.inputOn {
			return m.updateInput(msg)
		}
		m.status = ""
		if m.showHelp {
			m.showHelp = false
			return m, nil
		}
		if m.timeMode {
			return m.updateTime(msg)
		}
		switch msg.String() {
		case "ctrl+c":
			return m, tea.Quit
		case ":":
			m.inputOn, m.cmdOn = true, true
			m.input.Prompt, m.input.Placeholder = ":", ""
			m.input.SetValue("")
			return m, m.input.Focus()
		case "?":
			m.showHelp = true
			return m, nil
		case "/":
			m.inputOn, m.cmdOn = true, false
			m.input.Prompt, m.input.Placeholder = "/ ", filterPlaceholder
			m.input.SetValue(m.fText)
			m.input.CursorEnd()
			return m, m.input.Focus()
		case "t":
			if m.view != viewDetail {
				m.timeMode = true
				m.buildHist()
				m.hCur = m.h.Peak()
				m.hSel = -1
			}
			return m, nil
		case "0":
			m.winFrom, m.winTo = time.Time{}, time.Time{}
			m.refresh()
			m.buildHist()
			m.status = "zoom reset"
			return m, nil
		case "w":
			m.export()
			return m, nil
		case "M":
			n := len(m.marked)
			m.marked = map[*audit.Event]bool{}
			m.status = fmt.Sprintf("cleared %d marks", n)
			return m, nil
		}
		switch m.view {
		case viewGroups:
			return m.updateGroups(msg)
		case viewEvents:
			return m.updateEvents(msg)
		case viewDetail:
			return m.updateDetail(msg)
		}
	}
	return m, nil
}

const filterPlaceholder = "user:alice verb:delete res:secrets ns:prod code:>=400 -verb:watch text…"

func (m Model) updateInput(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "enter":
		if m.cmdOn {
			m.inputOn = false
			m.input.Blur()
			switch c := strings.TrimSpace(m.input.Value()); c {
			case "q", "q!", "quit", "qa", "wq", "x":
				return m, tea.Quit
			case "":
			default:
				m.status = "unknown command: " + c
			}
			return m, nil
		}
		f, err := audit.ParseFilter(m.input.Value())
		if err != nil {
			m.status = "filter error: " + err.Error()
			return m, nil
		}
		m.filter, m.fText = f, strings.TrimSpace(m.input.Value())
		m.inputOn = false
		m.input.Blur()
		m.refresh()
		m.buildHist()
		return m, nil
	case "esc":
		m.inputOn = false
		m.input.Blur()
		return m, nil
	}
	var cmd tea.Cmd
	m.input, cmd = m.input.Update(msg)
	return m, cmd
}

func (m Model) updateTime(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	n := len(m.h.Counts)
	switch msg.String() {
	case "left", "h":
		m.hCur = max(0, m.hCur-1)
	case "right", "l":
		m.hCur = min(n-1, m.hCur+1)
	case "shift+left", "H":
		m.hCur = max(0, m.hCur-10)
	case "shift+right", "L":
		m.hCur = min(n-1, m.hCur+10)
	case "home":
		m.hCur = 0
	case "end":
		m.hCur = n - 1
	case "p":
		m.hCur = m.h.Peak()
	case "[", " ":
		m.hSel = m.hCur
	case "]", "enter":
		a, b := m.hCur, m.hCur
		if m.hSel >= 0 {
			a, b = min(m.hSel, m.hCur), max(m.hSel, m.hCur)
		}
		m.winFrom = m.h.BucketTime(a)
		m.winTo = m.h.BucketTime(b + 1).Add(-time.Nanosecond)
		m.timeMode, m.hSel = false, -1
		m.refresh()
		m.buildHist()
		m.status = fmt.Sprintf("zoomed to %s – %s  (0 resets)", fmtTime(m.winFrom), fmtTime(m.winTo))
	case "esc", "t":
		m.timeMode, m.hSel = false, -1
	case "0":
		m.winFrom, m.winTo = time.Time{}, time.Time{}
		m.timeMode, m.hSel = false, -1
		m.refresh()
		m.buildHist()
	}
	return m, nil
}

func (m *Model) moveCursor(cur, off *int, n int, key string) bool {
	page := m.listHeight()
	switch key {
	case "up", "k":
		*cur--
	case "down", "j":
		*cur++
	case "pgup", "ctrl+u":
		*cur -= page
	case "pgdown", "ctrl+d":
		*cur += page
	case "home", "g":
		*cur = 0
	case "end", "G":
		*cur = n - 1
	default:
		return false
	}
	*cur = max(0, min(*cur, n-1))
	if *cur < *off {
		*off = *cur
	}
	if *cur >= *off+page {
		*off = *cur - page + 1
	}
	return true
}

func (m Model) updateGroups(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	k := msg.String()
	if m.moveCursor(&m.gCur, &m.gOff, len(m.groups), k) {
		if m.histSel {
			m.buildHist()
		}
		return m, nil
	}
	switch k {
	case "enter", "right":
		if m.gCur < len(m.groups) {
			m.group = m.groups[m.gCur]
			m.view, m.eCur, m.eOff = viewEvents, 0, 0
			m.sortEvents()
			m.buildHist()
		}
	case "b":
		m.groupBy = (m.groupBy + 1) % len(audit.GroupPresets)
		m.gCur, m.gOff = 0, 0
		m.refresh()
		m.status = "group by: " + string(audit.GroupPresets[m.groupBy])
	case "B":
		m.groupBy = (m.groupBy + len(audit.GroupPresets) - 1) % len(audit.GroupPresets)
		m.gCur, m.gOff = 0, 0
		m.refresh()
		m.status = "group by: " + string(audit.GroupPresets[m.groupBy])
	case "s":
		m.sortBy = (m.sortBy + 1) % 3
		m.refresh()
		m.status = "sort by: " + m.sortBy.String()
	case "H":
		m.histSel = !m.histSel
		m.buildHist()
	case " ":
		if m.gCur < len(m.groups) {
			g := m.groups[m.gCur]
			all := true
			for _, e := range g.Events {
				all = all && m.marked[e]
			}
			for _, e := range g.Events {
				if all {
					delete(m.marked, e)
				} else {
					m.marked[e] = true
				}
			}
			m.moveCursor(&m.gCur, &m.gOff, len(m.groups), "down")
		}
	case "esc":
		if m.fText != "" {
			m.filter, m.fText = nil, ""
			m.refresh()
			m.buildHist()
			m.status = "filter cleared"
		} else if m.zoomed() {
			m.winFrom, m.winTo = time.Time{}, time.Time{}
			m.refresh()
			m.buildHist()
			m.status = "zoom reset"
		}
	case "U", "R", "N", "V", "I":
		if m.gCur < len(m.groups) {
			m.pivot(m.groups[m.gCur].Sample, k)
		}
	}
	return m, nil
}

func (m Model) updateEvents(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if m.group == nil {
		m.view = viewGroups
		return m, nil
	}
	evs := m.evList
	k := msg.String()
	if m.moveCursor(&m.eCur, &m.eOff, len(evs), k) {
		return m, nil
	}
	switch k {
	case "s":
		m.eSort = (m.eSort + 1) % len(eventSorts)
		m.sortEvents()
		m.eCur, m.eOff = 0, 0
	case "esc", "left", "backspace":
		m.view = viewGroups
		m.buildHist()
	case "enter", "right":
		if m.eCur < len(evs) {
			m.openDetail(evs[m.eCur])
		}
	case " ":
		if m.eCur < len(evs) {
			e := evs[m.eCur]
			if m.marked[e] {
				delete(m.marked, e)
			} else {
				m.marked[e] = true
			}
			m.moveCursor(&m.eCur, &m.eOff, len(evs), "down")
		}
	case "U", "R", "N", "V", "I":
		if m.eCur < len(evs) {
			m.pivot(evs[m.eCur], k)
		}
	}
	return m, nil
}

func (m Model) updateDetail(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "esc", "left", "backspace":
		m.view = viewEvents
		m.buildHist()
		return m, nil
	case " ":
		if m.marked[m.detail] {
			delete(m.marked, m.detail)
		} else {
			m.marked[m.detail] = true
		}
		m.setDetailContent()
		return m, nil
	case "n", "N":
		if m.group != nil {
			d := 1
			if msg.String() == "N" {
				d = -1
			}
			m.eCur = max(0, min(len(m.evList)-1, m.eCur+d))
			m.ensureVisible()
			m.openDetail(m.evList[m.eCur])
		}
		return m, nil
	}
	var cmd tea.Cmd
	m.vp, cmd = m.vp.Update(msg)
	return m, cmd
}

// ensureVisible keeps both list cursors on screen and fills the screen after a resize.
func (m *Model) ensureVisible() {
	lh := m.listHeight()
	fix := func(cur, off *int, n int) {
		if *cur >= *off+lh {
			*off = *cur - lh + 1
		}
		if *cur < *off {
			*off = *cur
		}
		*off = max(0, min(*off, n-lh))
	}
	fix(&m.gCur, &m.gOff, len(m.groups))
	fix(&m.eCur, &m.eOff, len(m.evList))
}

func (m *Model) sortEvents() {
	if m.group == nil {
		m.evList = nil
		return
	}
	l := append([]*audit.Event(nil), m.group.Events...)
	switch m.eSort {
	case 1:
		sort.SliceStable(l, func(i, j int) bool { return l[i].Time.After(l[j].Time) })
	case 2:
		sort.SliceStable(l, func(i, j int) bool { return l[i].Latency > l[j].Latency })
	case 3:
		sort.SliceStable(l, func(i, j int) bool { return l[i].Code > l[j].Code })
	}
	m.evList = l
}

// pivot adds a filter term derived from an event, for drilling down.
func (m *Model) pivot(e *audit.Event, key string) {
	var t string
	switch key {
	case "U":
		t = "user:" + e.User
	case "R":
		t = "res:" + e.Resource
	case "N":
		t = "ns:" + e.Namespace
	case "V":
		t = "verb:" + e.Verb
	case "I":
		t = "ip:" + e.SourceIP
	}
	if strings.HasSuffix(t, ":") || strings.ContainsAny(t, " ") {
		m.status = "nothing to pivot on"
		return
	}
	text := strings.TrimSpace(m.fText + " " + t)
	f, err := audit.ParseFilter(text)
	if err != nil {
		m.status = err.Error()
		return
	}
	m.filter, m.fText = f, text
	m.view, m.group = viewGroups, nil
	m.refresh()
	m.buildHist()
	m.status = "filter += " + t
}

func (m *Model) openDetail(e *audit.Event) {
	m.detail, m.view = e, viewDetail
	m.setDetailContent()
	m.vp.GotoTop()
}

var jsonKeyRe = regexp.MustCompile(`(?m)^(\s*)("[^"]+")(:)`)

func (m *Model) setDetailContent() {
	e := m.detail
	var pretty bytes.Buffer
	if err := json.Indent(&pretty, e.Raw, "", "  "); err != nil {
		pretty.Write(e.Raw)
	}
	body := jsonKeyRe.ReplaceAllStringFunc(pretty.String(), func(s string) string {
		sm := jsonKeyRe.FindStringSubmatch(s)
		return sm[1] + sJSONKey.Render(sm[2]) + sm[3]
	})
	var b strings.Builder
	mark := ""
	if m.marked[e] {
		mark = sMark.Render(" [marked]")
	}
	b.WriteString(sBold.Render(audit.Sentence(e, 1)) + mark + "\n")
	b.WriteString(fmt.Sprintf("%s  %s  %s  latency %s  from %s  via %s\n",
		fmtTime(e.Time), codeStyle(e.Code).Render(codeStr(e.Code)+" "+e.Reason), e.Stage,
		fmtDur(e.Latency), dash(e.SourceIP), audit.ShortAgent(e.UserAgent)))
	if e.Message != "" {
		b.WriteString(sWarn.Render("message: "+e.Message) + "\n")
	}
	if e.Decision != "" {
		b.WriteString(sMuted.Render("authz decision: "+e.Decision) + "\n")
	}
	b.WriteString(sMuted.Render(strings.Repeat("─", max(10, m.width))) + "\n")
	b.WriteString(body)
	m.vp.SetContent(b.String())
}

// export writes marked events (or the current selection) as JSON lines.
func (m *Model) export() {
	var evs []*audit.Event
	what := "marked"
	if len(m.marked) > 0 {
		for e := range m.marked {
			evs = append(evs, e)
		}
		sort.Slice(evs, func(i, j int) bool { return evs[i].Time.Before(evs[j].Time) })
	} else {
		switch m.view {
		case viewDetail:
			evs, what = []*audit.Event{m.detail}, "current"
		case viewEvents:
			evs, what = m.evList, "group"
		default:
			evs, what = m.filtered, "filtered"
		}
	}
	name := fmt.Sprintf("kaudit-export-%s.jsonl", time.Now().Format("20060102-150405"))
	f, err := os.Create(name)
	if err != nil {
		m.status = "export failed: " + err.Error()
		return
	}
	defer f.Close()
	for _, e := range evs {
		f.Write(e.Raw)
		f.Write([]byte{'\n'})
	}
	m.status = fmt.Sprintf("wrote %d %s events to %s", len(evs), what, name)
}

const helpText = `kaudit – Kubernetes audit log explorer

VIEWS
  groups   similar requests merged (default: user + verb + resource + namespace + status class)
  events   individual requests of a group          (enter on a group)
  detail   full JSON of one request                (enter on an event)

NAVIGATION
  ↑/↓ j/k  move          pgup/pgdn  page       g/G  top/bottom
  enter    open          esc        back (in groups: clear filter, then zoom)
  :q       quit          ?          this help

GROUPING
  b / B    cycle group-by: similar, user, resource, verb, namespace, sourceIP, userAgent, status, none
  s        cycle sort – groups: count, latest, errors · events: time ↑, time ↓, latency, code
           (the active sort is highlighted in the toolbar and marked ▼/▲ in the column header)
  H        histogram shows all matching events / only the selected group

FILTER  (/ to edit, enter to apply, empty clears)
  user:alice  verb:delete  res:secrets  ns:prod  name:foo  ip:10.0.  agent:kubectl
  code:403  code:>=400  code:5xx  stage:ResponseComplete  uri:/api  group:apps
  -verb:watch  negate a term          other words: match anywhere in the raw JSON
  PIVOT = "show me everything else from this …": takes a value from the selected row and adds
  it to the filter, so you can jump from one suspicious request to all related ones.
    U user   R resource   N namespace   V verb   I source IP

TIME
  t        enter time mode: ←/→ move (H/L ×10), p jump to peak
           [ or space mark range start, ] or enter zoom into range (or the single bucket)
  0        reset zoom

INVESTIGATE
  space    mark event (or whole group)        M  clear all marks
  w        export marked events as JSONL (or, when nothing is marked: current event/group/filtered set)
  n / N    next / previous event in detail view

press any key to return`

// ---------- helpers ----------

func fmtTime(t time.Time) string { return t.Format("2006-01-02 15:04:05") }

func fmtShort(t time.Time) string {
	return t.Format("01-02 15:04")
}

func fmtDur(d time.Duration) string {
	switch {
	case d <= 0:
		return "-"
	case d < time.Millisecond:
		return fmt.Sprintf("%dµs", d.Microseconds())
	case d < time.Second:
		return fmt.Sprintf("%dms", d.Milliseconds())
	case d < time.Minute:
		return fmt.Sprintf("%.1fs", d.Seconds())
	}
	return d.Round(time.Second).String()
}

func fmtInt(n int) string {
	s := fmt.Sprint(n)
	if n < 1000 {
		return s
	}
	var b strings.Builder
	for i, r := range s {
		if i > 0 && (len(s)-i)%3 == 0 {
			b.WriteByte(',')
		}
		b.WriteRune(r)
	}
	return b.String()
}

func dash(s string) string {
	if s == "" {
		return "-"
	}
	return s
}

// truncate cuts an (optionally styled) string to w cells.
func truncate(s string, w int) string {
	if w <= 0 {
		return ""
	}
	if lipgloss.Width(s) <= w {
		return s
	}
	return lipgloss.NewStyle().MaxWidth(w-1).Render(s) + "…"
}

func padRight(s string, w int) string {
	if d := w - lipgloss.Width(s); d > 0 {
		return s + strings.Repeat(" ", d)
	}
	return s
}
