package audit

import (
	"bytes"
	"compress/gzip"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const sample = `{"auditID":"1","stage":"RequestReceived","verb":"get","user":{"username":"alice"},"objectRef":{"resource":"pods","namespace":"shop","name":"a"},"requestReceivedTimestamp":"2026-01-01T10:00:00Z"}
{"auditID":"1","stage":"ResponseComplete","verb":"get","user":{"username":"alice"},"objectRef":{"resource":"pods","namespace":"shop","name":"a"},"responseStatus":{"code":200},"requestReceivedTimestamp":"2026-01-01T10:00:00Z","stageTimestamp":"2026-01-01T10:00:00.050Z"}
{"auditID":"2","stage":"ResponseComplete","verb":"get","user":{"username":"alice"},"objectRef":{"resource":"pods","namespace":"shop","name":"b"},"responseStatus":{"code":200},"requestReceivedTimestamp":"2026-01-01T10:01:00Z"}
{"auditID":"3","stage":"ResponseComplete","verb":"list","user":{"username":"system:serviceaccount:kube-system:coredns"},"userAgent":"coredns/v1.11 (linux)","objectRef":{"resource":"endpointslices"},"responseStatus":{"code":200},"requestReceivedTimestamp":"2026-01-01T09:00:00Z"}
{"auditID":"4","stage":"ResponseComplete","verb":"get","user":{"username":"bob"},"objectRef":{"resource":"secrets","namespace":"prod","name":"x"},"responseStatus":{"code":403,"reason":"Forbidden"},"sourceIPs":["10.0.0.9"],"requestReceivedTimestamp":"2026-01-01T11:00:00Z"}
not json
{"broken":
`

func load(t *testing.T, stage string) *LoadResult {
	t.Helper()
	res := &LoadResult{}
	if err := Read(strings.NewReader(sample), LoadOptions{Stage: stage}, res); err != nil {
		t.Fatal(err)
	}
	return res
}

func ptrs(evs []Event) []*Event {
	out := make([]*Event, len(evs))
	for i := range evs {
		out[i] = &evs[i]
	}
	return out
}

func TestLoadStageAndBadLines(t *testing.T) {
	res := load(t, "ResponseComplete")
	if len(res.Events) != 4 || res.Skipped != 1 || res.BadLine != 2 {
		t.Fatalf("events=%d skipped=%d bad=%d", len(res.Events), res.Skipped, res.BadLine)
	}
	if res.Events[0].Latency.Milliseconds() != 50 {
		t.Errorf("latency = %v", res.Events[0].Latency)
	}
}

func TestLoadGzip(t *testing.T) {
	var buf bytes.Buffer
	zw := gzip.NewWriter(&buf)
	zw.Write([]byte(sample))
	zw.Close()
	p := filepath.Join(t.TempDir(), "a.log") // no .gz suffix: detection is by magic bytes
	os.WriteFile(p, buf.Bytes(), 0o644)
	res, err := LoadFiles([]string{p}, LoadOptions{})
	if err != nil || len(res.Events) != 5 {
		t.Fatalf("err=%v n=%d", err, len(res.Events))
	}
	if !res.Events[0].Time.Before(res.Events[1].Time) {
		t.Error("events not sorted by time")
	}
}

func TestGroupSimilarIgnoresNames(t *testing.T) {
	evs := ptrs(load(t, "ResponseComplete").Events)
	gs := BuildGroups(evs, BySimilar, SortCount)
	if len(gs) != 3 {
		t.Fatalf("groups = %d", len(gs))
	}
	if len(gs[0].Events) != 2 {
		t.Fatalf("top group has %d events", len(gs[0].Events))
	}
	if got := gs[0].Title(); got != "alice got pods (2 objects) in shop" {
		t.Errorf("title = %q", got)
	}
	gs = BuildGroups(evs, BySimilar, SortErrors)
	if gs[0].Errors != 1 {
		t.Error("errors-first sort failed")
	}
	if got := BuildGroups(evs, ByNone, SortCount); len(got) != 4 {
		t.Errorf("none grouping: %d", len(got))
	}
}

func TestSentenceAndShorteners(t *testing.T) {
	evs := ptrs(load(t, "ResponseComplete").Events)
	var dns *Event
	for _, e := range evs {
		if e.Verb == "list" {
			dns = e
		}
	}
	if got := Sentence(dns, 0); got != "sa:kube-system/coredns listed endpointslices in all namespaces" {
		t.Errorf("sentence = %q", got)
	}
	if ShortAgent("kubectl/v1.30.1 (darwin/arm64) kubernetes/x") != "kubectl" {
		t.Error("ShortAgent")
	}
}

func TestFilter(t *testing.T) {
	evs := ptrs(load(t, "ResponseComplete").Events)
	cases := map[string]int{
		"":                   4,
		"user:alice":         2,
		"code:>=400":         1,
		"code:4xx":           1,
		"code:200":           3,
		"-verb:get":          1,
		"ns:shop name:b":     1,
		"ip:10.0.":           1,
		"forbidden":          1, // free text in raw JSON
		"user:coredns":       1,
		"res:secrets -u:bob": 0,
	}
	for expr, want := range cases {
		f, err := ParseFilter(expr)
		if err != nil {
			t.Fatalf("%q: %v", expr, err)
		}
		n := 0
		for _, e := range evs {
			if f.Match(e) {
				n++
			}
		}
		if n != want {
			t.Errorf("%q matched %d, want %d", expr, n, want)
		}
	}
	if _, err := ParseFilter("code:abc"); err == nil {
		t.Error("expected error for bad code")
	}
}
