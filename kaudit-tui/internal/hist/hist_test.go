package hist

import (
	"strings"
	"testing"
	"time"
	"unicode/utf8"
)

func TestNiceStep(t *testing.T) {
	if s := NiceStep(3*time.Hour, 100); s != 2*time.Minute {
		t.Errorf("got %v", s)
	}
	if s := NiceStep(10*time.Second, 100); s != time.Second {
		t.Errorf("got %v", s)
	}
}

func TestBuildAndRender(t *testing.T) {
	base := time.Date(2026, 1, 1, 10, 0, 0, 0, time.UTC)
	var ts []time.Time
	for i := 0; i < 60; i++ {
		ts = append(ts, base.Add(time.Duration(i)*time.Minute))
	}
	for i := 0; i < 50; i++ {
		ts = append(ts, base.Add(30*time.Minute))
	}
	h := Build(ts, base, base.Add(59*time.Minute), 40)
	if h.Total != 110 || len(h.Counts) > 40 {
		t.Fatalf("total=%d buckets=%d", h.Total, len(h.Counts))
	}
	if !h.BucketTime(h.Peak()).Equal(base.Add(30 * time.Minute)) {
		t.Errorf("peak at %v", h.BucketTime(h.Peak()))
	}
	out := h.Render(RenderOpts{Height: 5, Cursor: -1, SelFrom: -1})
	lines := strings.Split(out, "\n")
	if len(lines) != 7 {
		t.Fatalf("lines = %d", len(lines))
	}
	w := utf8.RuneCountInString(lines[0])
	for _, l := range lines[:6] {
		if utf8.RuneCountInString(l) != w {
			t.Errorf("ragged line %q", l)
		}
	}
}
