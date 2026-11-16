// Package audit parses, filters and groups Kubernetes audit events.
package audit

import (
	"encoding/json"
	"strings"
	"time"
)

// rawEvent mirrors the subset of audit.k8s.io/v1 Event we care about.
type rawEvent struct {
	AuditID    string `json:"auditID"`
	Stage      string `json:"stage"`
	RequestURI string `json:"requestURI"`
	Verb       string `json:"verb"`
	User       struct {
		Username string   `json:"username"`
		Groups   []string `json:"groups"`
	} `json:"user"`
	ImpersonatedUser *struct {
		Username string `json:"username"`
	} `json:"impersonatedUser"`
	SourceIPs []string `json:"sourceIPs"`
	UserAgent string   `json:"userAgent"`
	ObjectRef *struct {
		Resource    string `json:"resource"`
		Namespace   string `json:"namespace"`
		Name        string `json:"name"`
		APIGroup    string `json:"apiGroup"`
		Subresource string `json:"subresource"`
	} `json:"objectRef"`
	ResponseStatus *struct {
		Code    int    `json:"code"`
		Reason  string `json:"reason"`
		Message string `json:"message"`
	} `json:"responseStatus"`
	RequestReceivedTimestamp time.Time         `json:"requestReceivedTimestamp"`
	StageTimestamp           time.Time         `json:"stageTimestamp"`
	Annotations              map[string]string `json:"annotations"`
}

// Event is a flattened, compact audit event.
type Event struct {
	AuditID     string
	Stage       string
	Verb        string
	User        string
	Impersonate string
	SourceIP    string
	UserAgent   string
	Resource    string // resource[/subresource]
	APIGroup    string
	Namespace   string
	Name        string
	URI         string
	Code        int
	Reason      string
	Message     string
	Time        time.Time
	Latency     time.Duration
	Decision    string // authorization decision annotation, if present
	Raw         []byte
}

func parseEvent(line []byte) (Event, error) {
	var r rawEvent
	if err := json.Unmarshal(line, &r); err != nil {
		return Event{}, err
	}
	e := Event{
		AuditID:   r.AuditID,
		Stage:     r.Stage,
		Verb:      r.Verb,
		User:      r.User.Username,
		UserAgent: r.UserAgent,
		URI:       r.RequestURI,
		Time:      r.RequestReceivedTimestamp,
		Raw:       append([]byte(nil), line...),
	}
	if e.Time.IsZero() {
		e.Time = r.StageTimestamp
	}
	if !r.StageTimestamp.IsZero() && !r.RequestReceivedTimestamp.IsZero() {
		e.Latency = r.StageTimestamp.Sub(r.RequestReceivedTimestamp)
	}
	if r.ImpersonatedUser != nil {
		e.Impersonate = r.ImpersonatedUser.Username
	}
	if len(r.SourceIPs) > 0 {
		e.SourceIP = r.SourceIPs[0]
	}
	if o := r.ObjectRef; o != nil {
		e.Resource = o.Resource
		if o.Subresource != "" {
			e.Resource += "/" + o.Subresource
		}
		e.APIGroup = o.APIGroup
		e.Namespace = o.Namespace
		e.Name = o.Name
	}
	if e.Resource == "" && r.RequestURI != "" {
		// non-resource URL like /healthz or /metrics
		p := r.RequestURI
		if i := strings.IndexByte(p, '?'); i >= 0 {
			p = p[:i]
		}
		e.Resource = p
	}
	if s := r.ResponseStatus; s != nil {
		e.Code, e.Reason, e.Message = s.Code, s.Reason, s.Message
	}
	e.Decision = r.Annotations["authorization.k8s.io/decision"]
	return e, nil
}

// StatusClass returns "2xx", "4xx", ... or "-" when unknown.
func (e *Event) StatusClass() string {
	if e.Code == 0 {
		return "-"
	}
	return string(rune('0'+e.Code/100)) + "xx"
}

// ShortUser makes service-account names compact.
func ShortUser(u string) string {
	if rest, ok := strings.CutPrefix(u, "system:serviceaccount:"); ok {
		return "sa:" + strings.Replace(rest, ":", "/", 1)
	}
	return u
}

// ShortAgent strips the version and platform noise from a user agent:
// "kubectl/v1.29.0 (linux/amd64) kubernetes/abc" -> "kubectl".
func ShortAgent(ua string) string {
	if ua == "" {
		return "-"
	}
	f := strings.Fields(ua)[0]
	if i := strings.IndexByte(f, '/'); i > 0 {
		f = f[:i]
	}
	return f
}

var pastTense = map[string]string{
	"get": "got", "list": "listed", "watch": "watched", "create": "created",
	"update": "updated", "patch": "patched", "delete": "deleted",
	"deletecollection": "deleted all", "proxy": "proxied", "impersonate": "impersonated",
}

// VerbPast returns a human readable past tense of a k8s verb.
func VerbPast(v string) string {
	if p, ok := pastTense[v]; ok {
		return p
	}
	return v
}
