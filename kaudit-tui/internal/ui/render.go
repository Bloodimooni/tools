package ui

import (
	"fmt"
	"sort"
	"strings"

	"github.com/charmbracelet/lipgloss"

	"kaudit-tui/internal/audit"
	"kaudit-tui/internal/hist"
)

var (
	groupSorts = []string{"count", "latest", "errors"}
	eventSorts = []string{"time ↑", "time ↓", "latency", "code"}
	sparkRunes = []rune(" ▁▂▃▄▅▆▇█")
	partRunes  = []rune(" ▏▎▍▌▋▊▉█")
)

// ---------- responsive layout ----------

// histHeight scales the chart with the terminal height.
func (m *Model) histHeight() int {
	switch {
	case m.height < 18:
		return 2
	case m.height < 26:
		return 4
	}
	return min(16, m.height/4)
}

// listHeight is what is left for list rows:
// header + chart(h + axis + labels + info) + toolbar + column header + footer.
func (m *Model) listHeight() int {
	used := 1 + m.histHeight() + 3 + 1 + 1 + 1
	if m.inputOn {
		used++
	}
	return max(1, m.height-used)
}

// ---------- top level ----------

func (m Model) View() string {
	if m.width == 0 {
		return "loading…"
	}
	if m.showHelp {
		return m.helpView()
	}
	if m.view == viewDetail {
		return m.viewDetail()
	}
	if m.h == nil {
		m.buildHist()
	}
	parts := []string{m.headerLine(), m.histView(), m.toolbar()}
	if m.view == viewEvents && m.group != nil {
		parts = append(parts, m.eventsView())
	} else {
		parts = append(parts, m.groupsView())
	}
	if m.inputOn {
		parts = append(parts, m.input.View())
	}
	parts = append(parts, m.footer())
	return strings.Join(parts, "\n")
}

func (m *Model) headerLine() string {
	parts := []string{sPill.Render("kaudit"), sMuted.Render(m.header)}
	if m.live > 0 {
		parts = append(parts, sErr.Render("●")+sMuted.Render(" live"))
	}
	parts = append(parts, sBold.Render(fmtInt(len(m.filtered)))+sMuted.Render("/"+fmtInt(len(m.all))+" events"))
	if m.fText != "" {
		parts = append(parts, sKey.Render("filter: ")+m.fText)
	}
	if m.zoomed() {
		parts = append(parts, sKey.Render("zoom: ")+fmtTime(m.winFrom)+" – "+fmtTime(m.winTo))
	}
	if len(m.marked) > 0 {
		parts = append(parts, sMark.Render(fmt.Sprintf("%d marked", len(m.marked))))
	}
	return truncate(strings.Join(parts, "  "), m.width)
}

func (m *Model) histView() string {
	h := m.h
	_, label := m.histEvents()
	opts := hist.RenderOpts{Height: m.histHeight(), Width: m.width, Cursor: -1, SelFrom: -1, SelTo: -1,
		Style: hist.Style{Bar: fn(sBar), Selected: fn(sBarSel), Cursor: fn(sBarCur), Axis: fn(sMuted)}}
	if m.timeMode {
		opts.Cursor = m.hCur
		if m.hSel >= 0 {
			opts.SelFrom, opts.SelTo = min(m.hSel, m.hCur), max(m.hSel, m.hCur)
		}
	}
	chart := h.Render(opts)
	var info string
	if m.timeMode {
		c := m.hCur
		info = fmt.Sprintf("%s %s – %s: %s requests", sKey.Render("▶ cursor"),
			fmtTime(h.BucketTime(c)), h.BucketTime(c+1).Format("15:04:05"), sBold.Render(fmtInt(h.Counts[c])))
		if m.hSel >= 0 {
			a, z := min(m.hSel, c), max(m.hSel, c)
			sum := 0
			for i := a; i <= z; i++ {
				sum += h.Counts[i]
			}
			info += fmt.Sprintf("   range: %d buckets, %s requests", z-a+1, sBold.Render(fmtInt(sum)))
		}
		info += sMuted.Render("   ←/→ move  [ start  ] / enter zoom  p peak  esc cancel")
	} else {
		p := h.Peak()
		info = sMuted.Render("requests per ") + sBold.Render(hist.FormatStep(h.Step)) +
			sMuted.Render(fmt.Sprintf(" (%s) · peak ", label)) + sBold.Render(fmtInt(h.Counts[p])) +
			sMuted.Render(" at "+fmtTime(h.BucketTime(p))+" · t explore time")
	}
	return chart + "\n" + truncate(info, m.width)
}

func tabs(items []string, active int) string {
	var b strings.Builder
	for i, it := range items {
		if i == active {
			b.WriteString(sTabOn.Render(it))
		} else {
			b.WriteString(sTabOff.Render(it))
		}
	}
	return b.String()
}

// toolbar shows the active grouping and sort as highlighted tabs; on narrow
// terminals it collapses to only the active ones.
func (m *Model) toolbar() string {
	var full, short string
	if m.view == viewEvents && m.group != nil {
		lead := sKey.Render("sort") + sMuted.Render(" s")
		full = lead + tabs(eventSorts, m.eSort)
		short = lead + " " + sTabOn.Render(eventSorts[m.eSort])
		title := "  " + sMuted.Render("esc ◀ ") + sBold.Render(m.group.Title()) +
			sMuted.Render(fmt.Sprintf("  (%s events)", fmtInt(len(m.evList))))
		full += title
		short += title
	} else {
		names := make([]string, len(audit.GroupPresets))
		for i, g := range audit.GroupPresets {
			names[i] = string(g)
		}
		g := sKey.Render("group") + sMuted.Render(" b")
		s := sKey.Render("sort") + sMuted.Render(" s")
		full = g + tabs(names, m.groupBy) + "  " + s + tabs(groupSorts, int(m.sortBy))
		short = g + " " + sTabOn.Render(names[m.groupBy]) + "  " + s + " " + sTabOn.Render(groupSorts[m.sortBy])
		if m.histSel {
			full += sMuted.Render("  chart: selected group (H)")
			short += sMuted.Render("  chart: group")
		}
	}
	if lipgloss.Width(full) <= m.width {
		return full
	}
	return truncate(short, m.width)
}

// ---------- groups table ----------

type groupCols struct {
	share, last, codes, act    bool
	shareW, actW, codesW, titW int
}

const countW, errW, lastW = 7, 5, 11

func (m *Model) groupLayout() groupCols {
	W := m.width
	c := groupCols{share: W >= 100, last: W >= 70, codes: W >= 125, act: W >= 150,
		shareW: clamp(W/12, 9, 18), actW: clamp(W/7, 12, 50), codesW: clamp(W/14, 12, 24)}
	calc := func() int {
		w := 2 + countW + 1 + errW + 2
		if c.share {
			w += c.shareW + 2
		}
		if c.last {
			w += lastW + 2
		}
		if c.codes {
			w += c.codesW + 2
		}
		if c.act {
			w += c.actW + 2
		}
		return W - w
	}
	// drop optional columns until the group text has room to breathe
	for c.titW = calc(); c.titW < 36; c.titW = calc() {
		switch {
		case c.act:
			c.act = false
		case c.codes:
			c.codes = false
		case c.share:
			c.share = false
		case c.last:
			c.last = false
		default:
			c.titW = max(5, c.titW)
			return c
		}
	}
	return c
}

func hcell(title string, w int, right, active bool, arrow string) string {
	if active {
		title += " " + arrow
	}
	s := pad(title, w, right)
	if active {
		return sHdrOn.Render(s)
	}
	return sHdr.Render(s)
}

func (m *Model) groupsView() string {
	c := m.groupLayout()
	lh := m.listHeight()
	var b strings.Builder
	b.WriteString("  ")
	b.WriteString(hcell("COUNT", countW, true, m.sortBy == audit.SortCount, "▼") + " ")
	b.WriteString(hcell("ERR", errW, true, m.sortBy == audit.SortErrors, "▼"))
	if c.share {
		b.WriteString("  " + hcell("SHARE", c.shareW, false, false, ""))
	}
	if c.last {
		b.WriteString("  " + hcell("LAST SEEN", lastW, false, m.sortBy == audit.SortLatest, "▼"))
	}
	b.WriteString("  " + hcell(strings.ToUpper(string(audit.GroupPresets[m.groupBy]))+" GROUP", c.titW, false, false, ""))
	if c.codes {
		b.WriteString("  " + hcell("STATUS CODES", c.codesW, false, false, ""))
	}
	if c.act {
		b.WriteString("  " + hcell("ACTIVITY", c.actW, false, false, ""))
	}
	b.WriteByte('\n')

	if len(m.groups) == 0 {
		b.WriteString(sMuted.Render("  no events match"))
		b.WriteString(strings.Repeat("\n", lh-1))
		return b.String()
	}
	maxCount := 0
	for _, g := range m.groups {
		maxCount = max(maxCount, len(g.Events))
	}
	end := min(len(m.groups), m.gOff+lh)
	for i := m.gOff; i < end; i++ {
		g := m.groups[i]
		if i == m.gCur {
			b.WriteString(sCursor.Render(padRight(m.groupRow(g, c, false, maxCount), m.width)))
		} else {
			b.WriteString(m.groupRow(g, c, true, maxCount))
		}
		if i < end-1 {
			b.WriteByte('\n')
		}
	}
	b.WriteString(strings.Repeat("\n", lh-(end-m.gOff)))
	return b.String()
}

// sty applies a style only when styled is set; the cursor row is rendered
// plain so its background highlight spans the full line.
func sty(styled bool, st lipgloss.Style, s string) string {
	if styled {
		return st.Render(s)
	}
	return s
}

func (m *Model) groupRow(g *audit.Group, c groupCols, styled bool, maxCount int) string {
	n := len(g.Events)
	nm := 0
	for _, e := range g.Events {
		if m.marked[e] {
			nm++
		}
	}
	mark := " "
	if nm == n {
		mark = "●"
	} else if nm > 0 {
		mark = "◐"
	}
	var b strings.Builder
	b.WriteString(sty(styled, sMark, mark) + " ")
	cnt := pad(fmtInt(n), countW, true)
	if m.sortBy == audit.SortCount {
		cnt = sty(styled, sSorted, cnt)
	}
	b.WriteString(cnt + " ")
	errS := pad("", errW, true)
	if g.Errors > 0 {
		errS = pad(fmtInt(g.Errors), errW, true)
		errS = sty(styled, codeStyle(worstCode(g)), errS)
	}
	if m.sortBy == audit.SortErrors && g.Errors > 0 {
		errS = sty(styled, lipgloss.NewStyle().Bold(true).Underline(true).Foreground(codeStyle(worstCode(g)).GetForeground()), pad(fmtInt(g.Errors), errW, true))
	}
	b.WriteString(errS)
	if c.share {
		b.WriteString("  " + shareCell(n, maxCount, len(m.filtered), c.shareW, styled))
	}
	if c.last {
		ls := fmtShort(g.Last)
		if m.sortBy == audit.SortLatest {
			ls = sty(styled, sSorted, ls)
		}
		b.WriteString("  " + ls)
	}
	b.WriteString("  " + m.titleCell(g, c.titW, styled))
	if c.codes {
		b.WriteString("  " + codesCell(g, c.codesW, styled))
	}
	if c.act {
		st := sBar
		if g.Errors > 0 {
			st = codeStyle(worstCode(g))
		}
		b.WriteString("  " + sty(styled, st, m.sparkline(g.Events, c.actW)))
	}
	return b.String()
}

// titleCell renders the group description; for "similar" groups the user is
// colored and the verb highlighted (writes amber, deletes red).
func (m *Model) titleCell(g *audit.Group, w int, styled bool) string {
	plain := pad(truncPlain(g.Title(), w), w, false)
	if !styled {
		return plain
	}
	if audit.GroupPresets[m.groupBy] != audit.BySimilar {
		return sBold.Render(plain)
	}
	e := g.Sample
	out, rest := "", plain
	if u := audit.ShortUser(e.User); u != "" && strings.HasPrefix(rest, u) {
		out, rest = sUser.Render(u), rest[len(u):]
	}
	v := " " + audit.VerbPast(e.Verb) + " "
	if i := strings.Index(rest, v); i >= 0 {
		out += rest[:i] + " " + verbStyle(e.Verb).Render(strings.TrimSpace(v)) + " "
		rest = rest[i+len(v):]
	}
	return out + rest
}

func shareCell(n, maxCount, total, w int, styled bool) string {
	barW := w - 5
	eighths := 0
	if maxCount > 0 {
		eighths = max(1, n*barW*8/maxCount)
	}
	bar := strings.Repeat("█", eighths/8)
	if r := eighths % 8; r > 0 {
		bar += string(partRunes[r])
	}
	bar = pad(bar, barW, false)
	pct := "<1%"
	if total > 0 && n*100/total >= 1 {
		pct = fmt.Sprintf("%d%%", n*100/total)
	}
	return sty(styled, sBar, bar) + " " + sty(styled, sMuted, pad(pct, 4, true))
}

func codesCell(g *audit.Group, w int, styled bool) string {
	type kv struct{ c, n int }
	var l []kv
	for c, n := range g.Codes {
		l = append(l, kv{c, n})
	}
	sort.Slice(l, func(i, j int) bool { return l[i].n > l[j].n })
	var b strings.Builder
	used := 0
	for i, x := range l {
		tok := fmt.Sprintf("%s×%s", codeStr(x.c), fmtInt(x.n))
		if i > 0 {
			tok = " " + tok
		}
		if used+len([]rune(tok)) > w-1 && i > 0 {
			b.WriteString(sty(styled, sMuted, " …"))
			used += 2
			break
		}
		b.WriteString(sty(styled, codeStyle(x.c), tok))
		used += len([]rune(tok))
	}
	if u := len(g.Users); u > 1 {
		if tok := fmt.Sprintf(" %d users", u); used+len(tok) <= w {
			b.WriteString(sty(styled, sMuted, tok))
			used += len(tok)
		}
	}
	return b.String() + strings.Repeat(" ", max(0, w-used))
}

// sparkline shows when the events happened within the current time window.
func (m *Model) sparkline(evs []*audit.Event, w int) string {
	from, to := m.window()
	span := int64(to.Sub(from))
	if span <= 0 {
		span = 1
	}
	counts := make([]int, w)
	mx := 0
	for _, e := range evs {
		i := int(int64(e.Time.Sub(from)) * int64(w) / span)
		i = max(0, min(w-1, i))
		counts[i]++
		mx = max(mx, counts[i])
	}
	out := make([]rune, w)
	for i, c := range counts {
		if c == 0 {
			out[i] = '·'
			continue
		}
		out[i] = sparkRunes[max(1, c*8/mx)]
	}
	return string(out)
}

// worstCode picks the most alarming error code: 5xx, then 401/403, then other 4xx.
func worstCode(g *audit.Group) int {
	rank := func(c int) int {
		switch {
		case c >= 500:
			return 3
		case c == 401 || c == 403:
			return 2
		case c >= 400:
			return 1
		}
		return 0
	}
	worst := 0
	for c := range g.Codes {
		if rank(c) > rank(worst) {
			worst = c
		}
	}
	return worst
}

// ---------- events table ----------

type eventCols struct {
	timeW, userW, objW int
	user, ip, agent    bool
}

const codeW, latW, verbW, ipW, agentW = 4, 8, 9, 15, 16

func (m *Model) eventLayout() eventCols {
	W := m.width
	c := eventCols{timeW: 19, user: W >= 100, ip: W >= 130, agent: W >= 155, userW: clamp(W/5, 16, 40)}
	if W < 90 {
		c.timeW = 14 // 01-02 15:04:05
	}
	calc := func() int {
		w := 2 + c.timeW + 2 + codeW + 2 + latW + 2 + verbW + 2
		if c.user {
			w += c.userW + 2
		}
		if c.ip {
			w += ipW + 2
		}
		if c.agent {
			w += agentW + 2
		}
		return W - w
	}
	for c.objW = calc(); c.objW < 24; c.objW = calc() {
		switch {
		case c.agent:
			c.agent = false
		case c.ip:
			c.ip = false
		case c.user:
			c.user = false
		default:
			c.objW = max(5, c.objW)
			return c
		}
	}
	return c
}

func (m *Model) eventsView() string {
	c := m.eventLayout()
	lh := m.listHeight()
	var b strings.Builder
	timeArrow := "▲"
	if m.eSort == 1 {
		timeArrow = "▼"
	}
	b.WriteString("  " + hcell("TIME", c.timeW, false, m.eSort <= 1, timeArrow))
	b.WriteString("  " + hcell("CODE", codeW+2, true, m.eSort == 3, "▼"))
	b.WriteString(hcell("LATENCY", latW+2, true, m.eSort == 2, "▼"))
	b.WriteString("  " + hcell("VERB", verbW, false, false, ""))
	b.WriteString("  " + hcell("OBJECT", c.objW, false, false, ""))
	if c.user {
		b.WriteString("  " + hcell("USER", c.userW, false, false, ""))
	}
	if c.ip {
		b.WriteString("  " + hcell("SOURCE IP", ipW, false, false, ""))
	}
	if c.agent {
		b.WriteString("  " + hcell("AGENT", agentW, false, false, ""))
	}
	b.WriteByte('\n')

	evs := m.evList
	end := min(len(evs), m.eOff+lh)
	for i := m.eOff; i < end; i++ {
		if i == m.eCur {
			b.WriteString(sCursor.Render(padRight(m.eventRow(evs[i], c, false), m.width)))
		} else {
			b.WriteString(m.eventRow(evs[i], c, true))
		}
		if i < end-1 {
			b.WriteByte('\n')
		}
	}
	b.WriteString(strings.Repeat("\n", lh-(end-m.eOff)))
	return b.String()
}

func (m *Model) eventRow(e *audit.Event, c eventCols, styled bool) string {
	mark := " "
	if m.marked[e] {
		mark = "●"
	}
	ts := e.Time.Format("2006-01-02 15:04:05")
	if c.timeW == 14 {
		ts = e.Time.Format("01-02 15:04:05")
	}
	if m.eSort <= 1 {
		ts = sty(styled, sSorted, ts)
	}
	code := pad(codeStr(e.Code), codeW+2, true)
	code = sty(styled, codeStyle(e.Code), code)
	lat := pad(fmtDur(e.Latency), latW+2, true)
	if m.eSort == 2 {
		lat = sty(styled, sSorted, lat)
	}
	obj := e.Resource
	if e.Namespace != "" || e.Name != "" {
		obj += " " + strings.Trim(e.Namespace+"/"+e.Name, "/")
	}
	var b strings.Builder
	b.WriteString(sty(styled, sMark, mark) + " " + ts + "  " + code + lat + "  ")
	b.WriteString(sty(styled, verbStyle(e.Verb), pad(truncPlain(e.Verb, verbW), verbW, false)) + "  ")
	b.WriteString(pad(truncPlain(obj, c.objW), c.objW, false))
	if c.user {
		b.WriteString("  " + sty(styled, sUser, pad(truncPlain(audit.ShortUser(e.User), c.userW), c.userW, false)))
	}
	if c.ip {
		b.WriteString("  " + pad(truncPlain(dash(e.SourceIP), ipW), ipW, false))
	}
	if c.agent {
		b.WriteString("  " + sty(styled, sMuted, pad(truncPlain(audit.ShortAgent(e.UserAgent), agentW), agentW, false)))
	}
	return b.String()
}

// ---------- detail, footer, help ----------

func (m *Model) viewDetail() string {
	pos := fmt.Sprintf("event %d/%d", m.eCur+1, len(m.evList))
	top := sPill.Render("kaudit") + "  " + sMuted.Render(pos)
	return top + "\n" + m.vp.View() + "\n" + m.footer()
}

func key(k, desc string) string { return sKey.Render(k) + " " + sMuted.Render(desc) }

type hint struct {
	text string
	prio int // higher numbers are dropped first when space runs out
}

func (m *Model) footer() string {
	if m.status != "" {
		return truncate(sWarn.Render(m.status), m.width)
	}
	pivotKeys := key("U", "user") + " " + key("R", "res") + " " + key("N", "ns") + " " + key("V", "verb") + " " + key("I", "ip")
	pivotLong := sKey.Render("pivot") + sMuted.Render(" = filter by this row's ") + pivotKeys
	pivotShort := sKey.Render("pivot→filter ") + pivotKeys
	var hs []hint
	switch m.view {
	case viewGroups:
		hs = []hint{{key("enter", "open"), 1}, {key("/", "filter"), 1}, {key("b", "group"), 2}, {key("s", "sort"), 2},
			{key("t", "time"), 2}, {key("space", "mark"), 3}, {key("w", "export"), 3}, {pivotLong, 1}, {key("?", "help"), 1}, {key(":q", "quit"), 4}}
	case viewEvents:
		hs = []hint{{key("enter", "details"), 1}, {key("esc", "back"), 1}, {key("s", "sort"), 2}, {key("/", "filter"), 2},
			{key("t", "time"), 3}, {key("space", "mark"), 3}, {key("w", "export"), 3}, {pivotLong, 1}, {key("?", "help"), 1}}
	default:
		hs = []hint{{key("↑↓ pgup/pgdn", "scroll"), 1}, {key("n/N", "next/prev"), 2}, {key("space", "mark"), 2},
			{key("w", "export"), 2}, {key("esc", "back"), 1}, {key(":q", "quit"), 3}}
	}
	join := func() string {
		parts := make([]string, len(hs))
		for i, h := range hs {
			parts[i] = h.text
		}
		return strings.Join(parts, "  ")
	}
	for lipgloss.Width(join()) > m.width {
		// first shorten the pivot explanation, then drop the least important hint
		shortened := false
		for i := range hs {
			if hs[i].text == pivotLong {
				hs[i].text, shortened = pivotShort, true
			}
		}
		if shortened {
			continue
		}
		worst := -1
		for i, h := range hs {
			if worst < 0 || h.prio >= hs[worst].prio {
				worst = i
			}
		}
		if hs[worst].prio <= 1 {
			break
		}
		hs = append(hs[:worst], hs[worst+1:]...)
	}
	return truncate(join(), m.width)
}

func (m *Model) helpView() string {
	lines := strings.Split(helpText, "\n")
	if len(lines) > m.height {
		lines = append(lines[:m.height-1], sMuted.Render("… (enlarge the terminal for the full help)"))
	}
	for i := range lines {
		lines[i] = truncate(lines[i], m.width)
	}
	return strings.Join(lines, "\n")
}

// ---------- text helpers ----------

func clamp(v, lo, hi int) int { return max(lo, min(hi, v)) }

// pad pads (or cuts) plain text to exactly w cells.
func pad(s string, w int, right bool) string {
	s = truncPlain(s, w)
	n := lipgloss.Width(s)
	if n >= w {
		return s
	}
	if right {
		return strings.Repeat(" ", w-n) + s
	}
	return s + strings.Repeat(" ", w-n)
}

// truncPlain cuts unstyled text to w cells, adding an ellipsis.
func truncPlain(s string, w int) string {
	if w <= 0 {
		return ""
	}
	if lipgloss.Width(s) <= w {
		return s
	}
	r := []rune(s)
	if len(r) > w-1 {
		r = r[:w-1]
	}
	return string(r) + "…"
}
