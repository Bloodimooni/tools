package audit

import (
	"bufio"
	"bytes"
	"compress/gzip"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"
)

// LoadResult holds parsed events plus parse statistics.
type LoadResult struct {
	Events  []Event
	BadLine int
	Skipped int // events dropped by the stage filter
}

// LoadOptions control which events are kept.
type LoadOptions struct {
	// Stage keeps only events of this stage ("" keeps all). Audit backends
	// usually log RequestReceived and ResponseComplete for the same request,
	// so defaulting to ResponseComplete avoids double counting.
	Stage string
}

// LoadFiles reads JSON-lines audit logs. "-" reads stdin. Files ending in .gz
// are decompressed. Events are returned sorted by time.
func LoadFiles(paths []string, opt LoadOptions) (*LoadResult, error) {
	res := &LoadResult{}
	for _, p := range paths {
		if err := loadOne(p, opt, res); err != nil {
			return nil, fmt.Errorf("%s: %w", p, err)
		}
	}
	sort.SliceStable(res.Events, func(i, j int) bool { return res.Events[i].Time.Before(res.Events[j].Time) })
	return res, nil
}

func loadOne(path string, opt LoadOptions, res *LoadResult) error {
	var r io.Reader
	if path == "-" {
		r = os.Stdin
	} else {
		f, err := os.Open(path)
		if err != nil {
			return err
		}
		defer f.Close()
		r = f
	}
	br := bufio.NewReaderSize(r, 1<<20)
	// detect gzip by magic bytes, not only by extension
	if magic, _ := br.Peek(2); len(magic) == 2 && magic[0] == 0x1f && magic[1] == 0x8b {
		gz, err := gzip.NewReader(br)
		if err != nil {
			return err
		}
		defer gz.Close()
		br = bufio.NewReaderSize(gz, 1<<20)
	}
	return Read(br, opt, res)
}

// Read parses JSON-lines from r into res.
func Read(r io.Reader, opt LoadOptions, res *LoadResult) error {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 1<<20), 64<<20)
	for sc.Scan() {
		line := bytes.TrimSpace(sc.Bytes())
		if len(line) == 0 || line[0] != '{' {
			if len(line) > 0 {
				res.BadLine++
			}
			continue
		}
		e, err := parseEvent(line)
		if err != nil {
			res.BadLine++
			continue
		}
		if opt.Stage != "" && e.Stage != "" && !strings.EqualFold(e.Stage, opt.Stage) {
			res.Skipped++
			continue
		}
		res.Events = append(res.Events, e)
	}
	return sc.Err()
}
