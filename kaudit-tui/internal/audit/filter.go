package audit

import (
	"bytes"
	"fmt"
	"strconv"
	"strings"
)

// Filter is a parsed filter expression. All terms must match (AND).
//
// Syntax (space separated):
//
//	user:alice  verb:delete  res:secrets  ns:prod  ip:10.0.  agent:kubectl
//	name:foo    code:403     code:>=400   code:5xx  stage:ResponseComplete
//	-verb:watch (negate any term)   plain words match anywhere in the raw JSON
//
// Field values are case-insensitive substring matches.
type Filter struct {
	terms []term
}

type term struct {
	field string
	value string
	neg   bool
	cmp   string // for code: "", ">", ">=", "<", "<=", "class"
	num   int
}

var fieldAliases = map[string]string{
	"user": "user", "u": "user",
	"verb": "verb", "v": "verb",
	"res": "res", "resource": "res", "r": "res",
	"ns": "ns", "namespace": "ns", "n": "ns",
	"name": "name",
	"ip":   "ip", "src": "ip",
	"agent": "agent", "ua": "agent",
	"code": "code", "status": "code", "c": "code",
	"stage": "stage",
	"group": "apigroup", "apigroup": "apigroup",
	"uri": "uri",
}

// ParseFilter parses an expression. An empty string matches everything.
func ParseFilter(s string) (*Filter, error) {
	f := &Filter{}
	for _, tok := range strings.Fields(s) {
		t := term{}
		if strings.HasPrefix(tok, "-") && len(tok) > 1 {
			t.neg, tok = true, tok[1:]
		}
		field, val, ok := strings.Cut(tok, ":")
		if canon, known := fieldAliases[strings.ToLower(field)]; ok && known {
			t.field, t.value = canon, strings.ToLower(val)
			if canon == "code" {
				if err := t.parseCode(); err != nil {
					return nil, err
				}
			}
		} else {
			t.field, t.value = "text", strings.ToLower(tok)
		}
		f.terms = append(f.terms, t)
	}
	return f, nil
}

func (t *term) parseCode() error {
	v := t.value
	if len(v) == 3 && strings.HasSuffix(v, "xx") {
		n, err := strconv.Atoi(v[:1])
		if err != nil {
			return fmt.Errorf("bad code class %q", v)
		}
		t.cmp, t.num = "class", n
		return nil
	}
	for _, op := range []string{">=", "<=", ">", "<"} {
		if strings.HasPrefix(v, op) {
			t.cmp, v = op, v[len(op):]
			break
		}
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		return fmt.Errorf("bad code %q", t.value)
	}
	t.num = n
	return nil
}

// Empty reports whether the filter has no terms.
func (f *Filter) Empty() bool { return f == nil || len(f.terms) == 0 }

// Match reports whether e satisfies all terms.
func (f *Filter) Match(e *Event) bool {
	if f == nil {
		return true
	}
	for i := range f.terms {
		if f.terms[i].match(e) == f.terms[i].neg {
			return false
		}
	}
	return true
}

func has(s, sub string) bool { return strings.Contains(strings.ToLower(s), sub) }

func (t *term) match(e *Event) bool {
	switch t.field {
	case "user":
		return has(e.User, t.value) || has(ShortUser(e.User), t.value) || has(e.Impersonate, t.value)
	case "verb":
		return has(e.Verb, t.value)
	case "res":
		return has(e.Resource, t.value)
	case "ns":
		return has(e.Namespace, t.value)
	case "name":
		return has(e.Name, t.value)
	case "ip":
		return has(e.SourceIP, t.value)
	case "agent":
		return has(e.UserAgent, t.value)
	case "stage":
		return has(e.Stage, t.value)
	case "apigroup":
		return has(e.APIGroup, t.value)
	case "uri":
		return has(e.URI, t.value)
	case "code":
		switch t.cmp {
		case "class":
			return e.Code/100 == t.num
		case ">":
			return e.Code > t.num
		case ">=":
			return e.Code >= t.num
		case "<":
			return e.Code < t.num
		case "<=":
			return e.Code <= t.num
		}
		return e.Code == t.num
	}
	return bytes.Contains(bytes.ToLower(e.Raw), []byte(t.value))
}
