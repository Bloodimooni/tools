# kaudit-tui

Interactive terminal explorer for Kubernetes API server audit logs (`audit.k8s.io/v1` Event, JSON lines).

- **Groups similar requests.** The default key is user + verb + resource + namespace + status class, and object names are ignored. Each group reads as one sentence, e.g. `sa:kube-system/coredns watched endpointslices in all namespaces`.
- **Shows a histogram of requests over time** in the terminal. You can move a cursor across it and zoom into a spike.
- **Drill down** from groups to the individual requests and then to the full JSON. Filter, pivot on a user/resource/namespace/verb/IP, mark events, and export them as JSONL for `jq`.

## Build / run

```sh
go build -o kaudit-tui .
./kaudit-tui /var/log/kubernetes/audit.log
./kaudit-tui audit-*.log.gz                 # gzip detected automatically
kubectl logs ... | ./kaudit-tui -           # stdin
./kaudit-tui --filter 'code:>=400' audit.log
./kaudit-tui --summary --top 30 audit.log   # non-interactive: histogram + top groups
```

### Remote and live sources

A source can be a local file, `-` for stdin, or `ssh://[user@]host[:port]/path`. Several sources are merged into one timeline. `-f` / `--follow` keeps streaming new events into the open UI (`tail -F`, so log rotation is handled):

```sh
# live view across all three control-plane nodes
./kaudit-tui -f --sudo \
  ssh://admin@master-1/var/log/kubernetes/audit.log \
  ssh://admin@master-2/var/log/kubernetes/audit.log \
  ssh://admin@master-3/var/log/kubernetes/audit.log

./kaudit-tui ssh://master-1/var/log/kubernetes/audit-2026-11-14.log.gz   # one-shot remote read
```

- ssh runs with `BatchMode=yes`, so it never prompts for a password. Use keys or an agent. Host aliases from `~/.ssh/config` work.
- `--sudo` runs the remote `tail`/`cat` via `sudo -n`, because audit logs are usually root-only. This needs passwordless sudo.
- `--backlog N` (default 10000): lines of existing log each source reads before following. `-1` reads the whole file.
- While streaming, the header shows `● live`. If a source dies (unreachable host, permission denied), the footer shows ssh's error and the other sources keep running.
- The histogram range grows with new events. Zooming in pins the window.

Flags:
- `--group-by similar|user|resource|verb|namespace|sourceIP|userAgent|status|none`
- `--stage`: default `ResponseComplete`. This avoids counting each request twice. Use `--stage ''` for all stages.
- `--filter`, `--summary`, `--top`, `--width`

Try it with the bundled synthetic log, which has an incident spike at 09:42: `./kaudit-tui testdata/sample.jsonl`.
Regenerate it with `go run ./internal/gen > testdata/sample.jsonl`.

## Keys

| key | action |
|---|---|
| ↑↓ / j k, pgup/pgdn, g/G | move |
| enter / esc | open / back. In the groups view, esc clears the filter, then the zoom |
| `/` | filter (see below) |
| `b` / `B` | cycle group-by |
| `s` | cycle sort. Groups: count, latest, errors. Events: time ↑, time ↓, latency, code. The active sort is the highlighted tab in the toolbar and is marked ▼/▲ in the column header |
| `H` | histogram shows all matching events or only the selected group |
| `t` | time mode: ←/→ move (H/L ×10), `p` jumps to the peak, `[` marks the start, `]`/enter zooms in. `0` resets |
| `U R N V I` | **pivot**: filter to everything sharing the selected row's user / resource / namespace / verb / IP. Use it to jump from one suspicious request to all related ones |
| space | mark an event, or a whole group. `M` clears all marks |
| `w` | export marked events as `./kaudit-export-<ts>.jsonl`. With nothing marked, it exports the current event, group, or filtered set |
| `n` / `N` | next / previous event in the detail view |
| `?` / `:q` | help / quit |

## Layout scaling

The UI adapts to the terminal size and re-layouts live on resize:
- **Chart:** its height is about ¼ of the terminal (2–16 rows), and buckets are stretched to fill the full width.
- **Group table:** columns are added as width allows: SHARE (bar + %) from 100 columns, STATUS CODES from 125, and a per-group ACTIVITY sparkline from 150. At narrower widths they are dropped so the group text keeps its room.
- **Event table:** USER, SOURCE IP and AGENT columns appear at 100 / 130 / 155 columns.
- **Footer:** hints are dropped by priority. The pivot explanation stays and only gets shorter.

## Filter syntax

Terms are separated by spaces and combined with AND. Values are case-insensitive substrings.

```
user:alice  verb:delete  res:secrets  ns:prod  name:foo  ip:10.0.  agent:kubectl
code:403  code:>=400  code:5xx  stage:ResponseComplete  uri:/apis  group:apps
-verb:watch      negate any term
anything-else    matched against the raw JSON line
```

## Layout

- `internal/audit`: parsing, filtering, grouping, and the human-readable text
- `internal/hist`: time bucketing and the bar chart
- `internal/ui`: the Bubble Tea TUI
