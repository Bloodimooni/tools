package audit

import (
	"fmt"
	"sort"
	"strings"
	"time"
)

// GroupBy names a grouping preset.
type GroupBy string

const (
	BySimilar   GroupBy = "similar" // user+verb+resource+namespace+status class
	ByUser      GroupBy = "user"
	ByResource  GroupBy = "resource"
	ByVerb      GroupBy = "verb"
	ByNamespace GroupBy = "namespace"
	BySourceIP  GroupBy = "sourceIP"
	ByAgent     GroupBy = "userAgent"
	ByStatus    GroupBy = "status"
	ByNone      GroupBy = "none" // every event is its own row
)

// GroupPresets in the order the UI cycles through them.
var GroupPresets = []GroupBy{BySimilar, ByUser, ByResource, ByVerb, ByNamespace, BySourceIP, ByAgent, ByStatus, ByNone}

// ParseGroupBy validates a group-by name.
func ParseGroupBy(s string) (GroupBy, error) {
	for _, g := range GroupPresets {
		if strings.EqualFold(string(g), s) {
			return g, nil
		}
	}
	names := make([]string, len(GroupPresets))
	for i, g := range GroupPresets {
		names[i] = string(g)
	}
	return "", fmt.Errorf("unknown group-by %q (valid: %s)", s, strings.Join(names, ", "))
}

// Group aggregates events that share a key.
type Group struct {
	Key    string
	Sample *Event // first event, used to describe the group
	Events []*Event
	Errors int // events with code >= 400
	Codes  map[int]int
	First  time.Time
	Last   time.Time
	Names  map[string]struct{} // distinct object names
	Users  map[string]struct{}
	by     GroupBy
}

func keyFor(e *Event, by GroupBy) string {
	switch by {
	case ByUser:
		return e.User
	case ByResource:
		return e.Resource
	case ByVerb:
		return e.Verb
	case ByNamespace:
		return e.Namespace
	case BySourceIP:
		return e.SourceIP
	case ByAgent:
		return ShortAgent(e.UserAgent)
	case ByStatus:
		return fmt.Sprint(e.Code)
	case ByNone:
		return e.AuditID + "\x00" + e.Time.String()
	default:
		return strings.Join([]string{e.User, e.Verb, e.Resource, e.Namespace, e.StatusClass()}, "\x00")
	}
}

// SortMode orders groups.
type SortMode int

const (
	SortCount SortMode = iota
	SortLatest
	SortErrors
)

func (s SortMode) String() string {
	return [...]string{"count", "latest", "errors"}[s]
}

// BuildGroups groups events (assumed time-sorted) and sorts the result.
func BuildGroups(events []*Event, by GroupBy, sortBy SortMode) []*Group {
	idx := map[string]*Group{}
	var out []*Group
	for _, e := range events {
		k := keyFor(e, by)
		g := idx[k]
		if g == nil {
			g = &Group{Key: k, Sample: e, First: e.Time, Codes: map[int]int{},
				Names: map[string]struct{}{}, Users: map[string]struct{}{}, by: by}
			idx[k] = g
			out = append(out, g)
		}
		g.Events = append(g.Events, e)
		g.Codes[e.Code]++
		if e.Code >= 400 {
			g.Errors++
		}
		if e.Time.Before(g.First) {
			g.First = e.Time
		}
		if e.Time.After(g.Last) {
			g.Last = e.Time
		}
		if e.Name != "" && len(g.Names) < 1000 {
			g.Names[e.Name] = struct{}{}
		}
		if len(g.Users) < 1000 {
			g.Users[e.User] = struct{}{}
		}
	}
	sort.SliceStable(out, func(i, j int) bool {
		a, b := out[i], out[j]
		switch sortBy {
		case SortLatest:
			return a.Last.After(b.Last)
		case SortErrors:
			if a.Errors != b.Errors {
				return a.Errors > b.Errors
			}
		}
		return len(a.Events) > len(b.Events)
	})
	return out
}

// Title is the short label for the group key.
func (g *Group) Title() string {
	e := g.Sample
	switch g.by {
	case ByUser:
		return ShortUser(e.User)
	case ByResource:
		return orDash(e.Resource)
	case ByVerb:
		return orDash(e.Verb)
	case ByNamespace:
		if e.Namespace == "" {
			return "(cluster-scoped)"
		}
		return e.Namespace
	case BySourceIP:
		return orDash(e.SourceIP)
	case ByAgent:
		return ShortAgent(e.UserAgent)
	case ByStatus:
		return fmt.Sprintf("%d %s", e.Code, e.Reason)
	}
	return Sentence(e, len(g.Names))
}

// Sentence describes an event (or a group of similar ones) in plain words:
// "sa:kube-system/coredns listed endpointslices in all namespaces".
func Sentence(e *Event, distinctNames int) string {
	var b strings.Builder
	b.WriteString(ShortUser(orDash(e.User)))
	if e.Impersonate != "" {
		b.WriteString(" (as " + ShortUser(e.Impersonate) + ")")
	}
	b.WriteString(" " + VerbPast(orDash(e.Verb)) + " ")
	res := orDash(e.Resource)
	if e.APIGroup != "" {
		res += "." + e.APIGroup
	}
	switch {
	case distinctNames == 1 && e.Name != "":
		b.WriteString(res + " " + e.Name)
	case distinctNames > 1:
		b.WriteString(fmt.Sprintf("%s (%d objects)", res, distinctNames))
	default:
		b.WriteString(res)
	}
	if strings.HasPrefix(e.Resource, "/") {
		return b.String() // non-resource URL
	}
	if e.Namespace != "" {
		b.WriteString(" in " + e.Namespace)
	} else if e.Verb == "list" || e.Verb == "watch" {
		b.WriteString(" in all namespaces")
	}
	return b.String()
}

func orDash(s string) string {
	if s == "" {
		return "-"
	}
	return s
}
