// Package hist buckets timestamps and renders a terminal bar chart.
package hist

import (
	"fmt"
	"strings"
	"time"
)

var niceSteps = []time.Duration{
	time.Second, 2 * time.Second, 5 * time.Second, 10 * time.Second, 15 * time.Second, 30 * time.Second,
	time.Minute, 2 * time.Minute, 5 * time.Minute, 10 * time.Minute, 15 * time.Minute, 30 * time.Minute,
	time.Hour, 2 * time.Hour, 3 * time.Hour, 6 * time.Hour, 12 * time.Hour, 24 * time.Hour,
	2 * 24 * time.Hour, 7 * 24 * time.Hour,
}

// Hist is a bucketed count series.
type Hist struct {
	Start  time.Time // start of bucket 0 (aligned to Step)
	Step   time.Duration
	Counts []int
	Max    int
	Total  int
}

// NiceStep returns the smallest "round" duration so that span fits in at most n buckets.
func NiceStep(span time.Duration, n int) time.Duration {
	if n < 1 {
		n = 1
	}
	for _, s := range niceSteps {
		if time.Duration(n)*s >= span {
			return s
		}
	}
	// fall back to whole days
	days := (span/time.Duration(n) + 24*time.Hour - 1) / (24 * time.Hour)
	return days * 24 * time.Hour
}

// Build buckets times (any order) between from and to into at most maxBuckets buckets.
func Build(times []time.Time, from, to time.Time, maxBuckets int) *Hist {
	if to.Before(from) {
		from, to = to, from
	}
	step := NiceStep(to.Sub(from)+time.Nanosecond, maxBuckets)
	start := from.Truncate(step)
	n := int(to.Sub(start)/step) + 1
	if n < 1 {
		n = 1
	}
	h := &Hist{Start: start, Step: step, Counts: make([]int, n)}
	for _, t := range times {
		if t.Before(start) || t.After(to) {
			continue
		}
		i := int(t.Sub(start) / step)
		if i >= n {
			i = n - 1
		}
		h.Counts[i]++
		h.Total++
	}
	for _, c := range h.Counts {
		h.Max = max(h.Max, c)
	}
	return h
}

// BucketTime returns the start time of bucket i.
func (h *Hist) BucketTime(i int) time.Time { return h.Start.Add(time.Duration(i) * h.Step) }

// Peak returns the index of the bucket with the highest count.
func (h *Hist) Peak() int {
	best := 0
	for i, c := range h.Counts {
		if c > h.Counts[best] {
			best = i
		}
	}
	return best
}

var blocks = []rune(" ▁▂▃▄▅▆▇█")

// Style lets callers colorize parts of the chart without this package
// depending on a styling library.
type Style struct {
	Bar      func(string) string // normal bars
	Selected func(string) string // bars inside the selected range
	Cursor   func(string) string // bar under the cursor
	Axis     func(string) string
}

func id(s string) string { return s }

// RenderOpts control the rendering.
type RenderOpts struct {
	Height  int // rows of bars
	Width   int // total width incl. the y-axis labels; 0 = one column per bucket
	Cursor  int // -1 for none
	SelFrom int // selected range, -1 for none
	SelTo   int
	Style   Style
}

const (
	kBar = iota
	kSel
	kCur
)

// Render draws the histogram. Buckets are stretched over the available
// width, so the chart always fills the terminal; when a bucket gets three or
// more columns a one-column gap separates it from its neighbour.
func (h *Hist) Render(o RenderOpts) string {
	st := o.Style
	for _, f := range []*func(string) string{&st.Bar, &st.Selected, &st.Cursor, &st.Axis} {
		if *f == nil {
			*f = id
		}
	}
	o.Height = max(1, o.Height)
	n := len(h.Counts)
	maxLabel := fmt.Sprint(h.Max)
	lw := len(maxLabel)
	pw := max(n, o.Width-lw-2)
	col2b := make([]int, pw)
	for c := range col2b {
		col2b[c] = c * n / pw
	}
	gaps := pw/n >= 3
	styleOf := []func(string) string{st.Bar, st.Selected, st.Cursor}

	var b strings.Builder
	for row := o.Height - 1; row >= 0; row-- {
		label := strings.Repeat(" ", lw)
		switch {
		case row == o.Height-1:
			label = fmt.Sprintf("%*s", lw, maxLabel)
		case row == o.Height/2 && o.Height >= 5:
			label = fmt.Sprintf("%*d", lw, h.Max/2)
		}
		b.WriteString(st.Axis(label + " │"))
		var run strings.Builder
		kind := -1
		for c := 0; c < pw; c++ {
			i := col2b[c]
			var eighths int
			if h.Max > 0 {
				eighths = (h.Counts[i]*o.Height*8 + h.Max - 1) / h.Max
			}
			fill := max(0, min(8, eighths-row*8))
			ch := string(blocks[fill])
			k := kBar
			switch {
			case i == o.Cursor:
				k = kCur
				if fill == 0 {
					ch = "┊"
				}
			case o.SelFrom >= 0 && i >= o.SelFrom && i <= o.SelTo:
				k = kSel
				if fill == 0 {
					ch = "░"
				}
			}
			if gaps && c+1 < pw && col2b[c+1] != i && k == kBar {
				ch = " "
			}
			if k != kind {
				if run.Len() > 0 {
					b.WriteString(styleOf[kind](run.String()))
					run.Reset()
				}
				kind = k
			}
			run.WriteString(ch)
		}
		if run.Len() > 0 {
			b.WriteString(styleOf[kind](run.String()))
		}
		b.WriteByte('\n')
	}
	labels, ticks := h.axis(col2b)
	pad := strings.Repeat(" ", lw)
	b.WriteString(st.Axis(pad + " └" + ticks))
	b.WriteByte('\n')
	b.WriteString(st.Axis(pad + "  " + labels))
	return b.String()
}

// axis returns time labels and a tick line, labels placed at bucket starts
// without overlapping.
func (h *Hist) axis(col2b []int) (string, string) {
	n, pw := len(h.Counts), len(col2b)
	format := "15:04"
	if h.Step < time.Minute {
		format = "15:04:05"
	}
	if h.Step*time.Duration(n) > 36*time.Hour {
		format = "01-02 15:04"
	}
	if h.Step >= 24*time.Hour {
		format = "01-02"
	}
	lab := []rune(strings.Repeat(" ", pw))
	tick := []rune(strings.Repeat("─", pw))
	next := 0
	for c := 0; c < pw; c++ {
		if c < next || (c > 0 && col2b[c] == col2b[c-1]) {
			continue
		}
		s := []rune(h.BucketTime(col2b[c]).Format(format))
		if c+len(s) > pw {
			break
		}
		copy(lab[c:], s)
		tick[c] = '┬'
		next = c + len(s) + 3
	}
	return strings.TrimRight(string(lab), " "), string(tick)
}

// FormatStep renders a bucket size compactly ("5m", "1h", "2d").
func FormatStep(d time.Duration) string {
	switch {
	case d >= 24*time.Hour && d%(24*time.Hour) == 0:
		return fmt.Sprintf("%dd", d/(24*time.Hour))
	case d >= time.Hour && d%time.Hour == 0:
		return fmt.Sprintf("%dh", d/time.Hour)
	case d >= time.Minute && d%time.Minute == 0:
		return fmt.Sprintf("%dm", d/time.Minute)
	}
	return d.String()
}
