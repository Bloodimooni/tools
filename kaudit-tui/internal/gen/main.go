// Command gen writes a realistic synthetic audit log for testing:
//
//	go run ./internal/gen > testdata/sample.jsonl
package main

import (
	"encoding/json"
	"fmt"
	"math/rand"
	"os"
	"time"
)

type actor struct {
	user, agent, ip string
	verb, res, ns   string
	names           []string
	code            int
	weight          int
}

func main() {
	r := rand.New(rand.NewSource(42))
	start := time.Date(2026, 11, 14, 8, 0, 0, 0, time.UTC)
	actors := []actor{
		{"system:serviceaccount:kube-system:coredns", "coredns/v1.11.1 (linux/amd64)", "10.244.0.3", "watch", "endpointslices", "", nil, 200, 6},
		{"system:kube-controller-manager", "kube-controller-manager/v1.30.2 (linux/amd64) kubernetes/39683505", "172.18.0.2", "get", "leases", "kube-system", []string{"kube-controller-manager"}, 200, 12},
		{"system:kube-scheduler", "kube-scheduler/v1.30.2 (linux/amd64)", "172.18.0.2", "update", "leases", "kube-system", []string{"kube-scheduler"}, 200, 12},
		{"system:node:worker-1", "kubelet/v1.30.2 (linux/amd64)", "172.18.0.3", "patch", "nodes/status", "", []string{"worker-1"}, 200, 8},
		{"system:node:worker-1", "kubelet/v1.30.2 (linux/amd64)", "172.18.0.3", "get", "pods", "shop", []string{"web-7d9f-abcde", "web-7d9f-fghij", "db-0"}, 200, 10},
		{"system:serviceaccount:argocd:argocd-application-controller", "argocd-application-controller/v2.11.0", "10.244.1.9", "list", "deployments", "", nil, 200, 4},
		{"alice@example.com", "kubectl/v1.30.1 (darwin/arm64) kubernetes/1234", "203.0.113.7", "get", "pods", "shop", []string{"web-7d9f-abcde", "db-0"}, 200, 2},
		{"alice@example.com", "kubectl/v1.30.1 (darwin/arm64) kubernetes/1234", "203.0.113.7", "get", "pods/log", "shop", []string{"web-7d9f-abcde"}, 200, 1},
		{"bob@example.com", "kubectl/v1.29.0 (linux/amd64) kubernetes/5678", "198.51.100.4", "list", "secrets", "shop", nil, 403, 1},
		{"system:anonymous", "curl/8.5.0", "192.0.2.66", "get", "/version", "", nil, 401, 1},
		{"system:serviceaccount:monitoring:prometheus", "Prometheus/2.51.0", "10.244.2.4", "get", "/metrics", "", nil, 200, 3},
	}
	total := 0
	for _, a := range actors {
		total += a.weight
	}
	pick := func() actor {
		n := r.Intn(total)
		for _, a := range actors {
			if n < a.weight {
				return a
			}
			n -= a.weight
		}
		return actors[0]
	}
	enc := json.NewEncoder(os.Stdout)
	id := 0
	emit := func(a actor, t time.Time, code int) {
		id++
		lat := time.Duration(r.Intn(40)+1) * time.Millisecond
		ev := map[string]any{
			"kind": "Event", "apiVersion": "audit.k8s.io/v1", "level": "Metadata",
			"auditID":                  fmt.Sprintf("%08x-0000-4000-8000-%012x", id, id),
			"stage":                    "ResponseComplete",
			"verb":                     a.verb,
			"user":                     map[string]any{"username": a.user, "groups": []string{"system:authenticated"}},
			"sourceIPs":                []string{a.ip},
			"userAgent":                a.agent,
			"responseStatus":           map[string]any{"metadata": map[string]any{}, "code": code},
			"requestReceivedTimestamp": t.Format(time.RFC3339Nano),
			"stageTimestamp":           t.Add(lat).Format(time.RFC3339Nano),
			"annotations":              map[string]string{"authorization.k8s.io/decision": "allow"},
		}
		if a.res[0] == '/' {
			ev["requestURI"] = a.res
		} else {
			name := ""
			if len(a.names) > 0 {
				name = a.names[r.Intn(len(a.names))]
			}
			res, sub := a.res, ""
			for i := range res {
				if res[i] == '/' {
					res, sub = a.res[:i], a.res[i+1:]
					break
				}
			}
			ref := map[string]any{"resource": res, "apiVersion": "v1"}
			if sub != "" {
				ref["subresource"] = sub
			}
			if a.ns != "" {
				ref["namespace"] = a.ns
			}
			if name != "" {
				ref["name"] = name
			}
			if res == "deployments" {
				ref["apiGroup"] = "apps"
			}
			if res == "leases" {
				ref["apiGroup"] = "coordination.k8s.io"
			}
			ev["objectRef"] = ref
			ev["requestURI"] = "/api/v1/" + a.res
		}
		if code >= 400 {
			ev["responseStatus"] = map[string]any{"code": code, "reason": map[int]string{401: "Unauthorized", 403: "Forbidden", 500: "InternalError"}[code],
				"message": fmt.Sprintf("%s is not allowed to %s %s", a.user, a.verb, a.res)}
			ev["annotations"] = map[string]string{"authorization.k8s.io/decision": "forbid"}
		}
		// also emit the RequestReceived stage, like a real apiserver does
		rr := map[string]any{}
		for k, v := range ev {
			rr[k] = v
		}
		rr["stage"] = "RequestReceived"
		delete(rr, "responseStatus")
		rr["stageTimestamp"] = t.Format(time.RFC3339Nano)
		enc.Encode(rr)
		enc.Encode(ev)
	}
	// 3 hours of baseline traffic
	for t := start; t.Before(start.Add(3 * time.Hour)); t = t.Add(time.Duration(r.Intn(8000)+500) * time.Millisecond) {
		a := pick()
		emit(a, t, a.code)
	}
	// incident: a burst of forbidden secret reads + some 500s around 09:42
	burst := start.Add(102 * time.Minute)
	for i := 0; i < 400; i++ {
		t := burst.Add(time.Duration(r.Intn(240)) * time.Second)
		a := actors[8]
		a.verb = []string{"list", "get", "get"}[r.Intn(3)]
		a.ns = []string{"shop", "kube-system", "default"}[r.Intn(3)]
		emit(a, t, 403)
	}
	for i := 0; i < 60; i++ {
		t := burst.Add(time.Duration(r.Intn(600)) * time.Second)
		emit(actors[5], t, 500)
	}
}
