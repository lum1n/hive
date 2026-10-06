package agents

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/lum1n/hive/internal/config"
	"github.com/lum1n/hive/internal/execx"
	"github.com/lum1n/hive/internal/sshx"
)

type Client struct {
	Config  config.Config
	SSH     sshx.Options
	Runner  execx.Runner
	Timeout time.Duration
}

func (c Client) run(ctx context.Context, host config.Host, script string) execx.Result {
	timeout := c.Timeout
	if timeout <= 0 {
		timeout = 12 * time.Second
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	if !host.Local && (strings.HasPrefix(host.Destination(), "-") || strings.ContainsAny(host.Destination(), "\x00\r\n")) {
		return execx.Result{Err: errors.New("invalid configured SSH destination")}
	}
	run := c.Runner
	if run == nil {
		run = execx.Bounded(MaxInventoryBytes, 8192)
	}
	name, args := invocation(c.SSH, host, script)
	result := run(ctx, name, args...)
	if len(result.Stdout) > MaxInventoryBytes || len(result.Stderr) > 8192 {
		return execx.Result{Err: execx.ErrOutputLimit}
	}
	if ctx.Err() != nil {
		return execx.Result{Err: ctx.Err()}
	}
	return result
}

func commandFailure(result execx.Result) *Failure {
	switch {
	case errors.Is(result.Err, execx.ErrOutputLimit):
		return failure("output_limit", "remote output exceeded its limit")
	case errors.Is(result.Err, context.DeadlineExceeded):
		return failure("timeout", "host operation timed out")
	case errors.Is(result.Err, context.Canceled):
		return failure("cancelled", "host operation was cancelled")
	}
	message := strings.ToLower(string(result.Stderr))
	for _, marker := range []string{"permission denied", "host key verification", "authentication failed", "too many authentication", "publickey"} {
		if strings.Contains(message, marker) {
			return failure("auth", "SSH authentication or host-key verification failed")
		}
	}
	switch execx.ExitCode(result.Err) {
	case 4:
		return failure("discovery", "host discovery tools unavailable")
	case 5:
		return failure("output_limit", "discovery or capture exceeded its limit")
	case 6:
		return failure("unavailable", "configured tmux server unavailable")
	case 7:
		return failure("protocol", "invalid tmux metadata")
	case 8:
		return failure("stale", "tmux server changed; rediscover agents")
	case 9:
		return failure("gone", "agent pane no longer available")
	case 255:
		return failure("offline", "SSH host unavailable")
	}
	return failure("unavailable", "host operation failed")
}

func (c Client) List(ctx context.Context, hostID string) (ListResponse, error) {
	hosts := c.Config.Hosts
	if hostID != "" {
		host, ok := c.Config.Host(hostID)
		if !ok {
			return ListResponse{}, fmt.Errorf("host is not configured")
		}
		hosts = []config.Host{host}
	}
	if len(hosts) > 64 {
		return ListResponse{}, fmt.Errorf("agent listing supports at most 64 hosts; select one with --host")
	}
	response := ListResponse{Version: Version, Time: time.Now().UTC(), Hosts: make([]HostResult, len(hosts))}
	sem := make(chan struct{}, 4)
	var wg sync.WaitGroup
	for index, host := range hosts {
		wg.Add(1)
		go func(index int, host config.Host) {
			defer wg.Done()
			select {
			case sem <- struct{}{}:
				defer func() { <-sem }()
			case <-ctx.Done():
				response.Hosts[index] = hostResult(host, commandFailure(execx.Result{Err: ctx.Err()}))
				return
			}
			result := c.run(ctx, host, inventoryScript(host))
			if result.Err != nil {
				response.Hosts[index] = hostResult(host, commandFailure(result))
				return
			}
			servers, err := parseInventory(result.Stdout, host.ID, time.Now().UTC())
			if err != nil {
				response.Hosts[index] = hostResult(host, failure("protocol", "invalid discovery response"))
				return
			}
			response.Hosts[index] = hostResult(host, nil)
			response.Hosts[index].Servers = servers
			for _, server := range servers {
				if server.Error != nil {
					response.Hosts[index].Status = "degraded"
				}
			}
		}(index, host)
	}
	wg.Wait()
	return response, nil
}

func hostResult(host config.Host, err *Failure) HostResult {
	status := "online"
	if err != nil {
		status = "unavailable"
		if err.Code == "auth" || err.Code == "offline" {
			status = err.Code
		}
	}
	return HostResult{Host: host.ID, Label: display(host.Display()), Local: host.Local,
		Status: status, Error: err, Servers: []Server{}}
}

func decoded(value string, limit int) (string, error) {
	if len(value) > base64.StdEncoding.EncodedLen(limit) {
		return "", fmt.Errorf("encoded field exceeds limit")
	}
	raw, err := base64.StdEncoding.DecodeString(value)
	if err != nil || len(raw) > limit {
		return "", fmt.Errorf("invalid encoded field")
	}
	return string(raw), nil
}

func protocol(raw []byte) ([][]string, error) {
	if len(raw) > MaxInventoryBytes {
		return nil, fmt.Errorf("response exceeds limit")
	}
	lines := strings.Split(strings.TrimSuffix(string(raw), "\n"), "\n")
	if len(lines) == 0 || lines[0] != "V\t1" {
		return nil, fmt.Errorf("unsupported probe protocol")
	}
	if len(lines) > 8192 {
		return nil, fmt.Errorf("too many probe records")
	}
	records := make([][]string, 0, len(lines)-1)
	for _, line := range lines[1:] {
		records = append(records, strings.Split(line, "\t"))
	}
	return records, nil
}

func parseProcesses(records [][]string) (map[int]process, error) {
	processes := map[int]process{}
	arguments := map[int]bool{}
	seen := false
	for _, fields := range records {
		if fields[0] == "P" {
			if len(fields) != 2 || seen {
				return nil, fmt.Errorf("invalid process snapshot")
			}
			seen = true
			text, err := decoded(fields[1], 2<<20)
			if err != nil {
				return nil, err
			}
			for line := range strings.SplitSeq(text, "\n") {
				parts := strings.Fields(line)
				if len(parts) < 3 {
					return nil, fmt.Errorf("invalid process record")
				}
				pid, err := strconv.Atoi(parts[0])
				parent, parentErr := strconv.Atoi(parts[1])
				if err != nil || parentErr != nil || pid < 1 || parent < 0 {
					return nil, fmt.Errorf("invalid process identity")
				}
				if _, duplicate := processes[pid]; duplicate || len(processes) >= 8192 {
					return nil, fmt.Errorf("invalid process snapshot size or identity")
				}
				processes[pid] = process{parent: parent, command: strings.Join(parts[2:], " ")}
			}
		} else if fields[0] == "A" {
			if len(fields) != 3 || !seen {
				return nil, fmt.Errorf("invalid process arguments record")
			}
			pid, err := strconv.Atoi(fields[1])
			p, ok := processes[pid]
			args, argsErr := decoded(fields[2], 8192)
			if err != nil || argsErr != nil || !ok || arguments[pid] || len(arguments) >= 128 {
				return nil, fmt.Errorf("invalid process arguments")
			}
			arguments[pid] = true
			p.args = args
			processes[pid] = p
		}
	}
	if !seen {
		return nil, fmt.Errorf("missing process snapshot")
	}
	return processes, nil
}

func expandInventory(records [][]string) ([][]string, error) {
	result := make([][]string, 0, len(records))
	panes := 0
	for _, fields := range records {
		if fields[0] != "I" {
			if fields[0] == "N" {
				panes++
				if panes > 4096 {
					return nil, fmt.Errorf("too many pane memberships")
				}
			}
			result = append(result, fields)
			continue
		}
		if len(fields) != 2 {
			return nil, fmt.Errorf("invalid metadata batch")
		}
		text, err := decoded(fields[1], MaxInventoryBytes)
		if err != nil {
			return nil, err
		}
		for text != "" {
			panes++
			if panes > 4096 {
				return nil, fmt.Errorf("too many pane memberships")
			}
			header, rest, ok := strings.Cut(text, "\t")
			parts := strings.Fields(header)
			if !ok || len(parts) != 7 {
				return nil, fmt.Errorf("invalid metadata header")
			}
			row := append([]string{"N"}, parts...)
			for range 5 {
				colon := strings.IndexByte(rest, ':')
				if colon < 1 || colon > 5 {
					return nil, fmt.Errorf("invalid metadata length")
				}
				for _, digit := range rest[:colon] {
					if digit < '0' || digit > '9' {
						return nil, fmt.Errorf("invalid metadata length")
					}
				}
				size, err := strconv.Atoi(rest[:colon])
				rest = rest[colon+1:]
				if err != nil || size > 16384 || len(rest) <= size || rest[size] != ',' {
					return nil, fmt.Errorf("invalid metadata field")
				}
				row = append(row, base64.StdEncoding.EncodeToString([]byte(rest[:size])))
				rest = rest[size+1:]
			}
			if rest != "" {
				if rest[0] != '\n' {
					return nil, fmt.Errorf("invalid metadata boundary")
				}
				rest = rest[1:]
			}
			result = append(result, row)
			text = rest
		}
	}
	return result, nil
}

func parseInventory(raw []byte, host string, now time.Time) ([]Server, error) {
	records, err := protocol(raw)
	if err != nil {
		return nil, err
	}
	records, err = expandInventory(records)
	if err != nil {
		return nil, err
	}
	processes, err := parseProcesses(records)
	if err != nil {
		return nil, err
	}
	servers := []Server{}
	current := -1
	bySocket := map[string]int{}
	for _, fields := range records {
		switch fields[0] {
		case "P", "A":
		case "S", "E":
			if len(fields) != 3 {
				return nil, fmt.Errorf("invalid server record")
			}
			socket, err := decoded(fields[1], 1024)
			if err != nil {
				return nil, err
			}
			if fields[0] == "E" {
				if fields[2] != "unavailable" || !strings.HasPrefix(socket, "/") {
					return nil, fmt.Errorf("invalid server error")
				}
				servers = append(servers, Server{Socket: socket, Status: "unavailable",
					Error: failure("unavailable", "tmux server unavailable"), Agents: []Agent{}})
				current = -1
				continue
			}
			ref := Reference{Version: Version, Host: host, Socket: socket, Generation: fields[2], Pane: "%0"}
			if err := ref.Validate(); err != nil {
				return nil, err
			}
			if index, ok := bySocket[socket]; ok {
				current = index
				if servers[index].Generation != fields[2] {
					return nil, fmt.Errorf("server generation changed")
				}
				continue
			}
			current = len(servers)
			bySocket[socket] = current
			servers = append(servers, Server{Socket: socket, Generation: fields[2], Status: "online", Agents: []Agent{}})
		case "N":
			if current < 0 || len(fields) != 13 || !paneID.MatchString(fields[1]) ||
				!windowID.MatchString(fields[2]) || !sessionID.MatchString(fields[3]) {
				return nil, fmt.Errorf("invalid pane identity")
			}
			for _, flag := range fields[5:8] {
				if flag != "0" && flag != "1" {
					return nil, fmt.Errorf("invalid pane flag")
				}
			}
			values := make([]string, 5)
			for i, field := range fields[8:13] {
				values[i], err = decoded(field, 16384)
				if err != nil {
					return nil, err
				}
			}
			override, command, directory, name, title := values[0], values[1], values[2], values[3], values[4]
			if fields[6] == "1" || fields[7] == "1" || override == "off" {
				continue
			}
			pid, err := strconv.Atoi(fields[4])
			if err != nil || pid < 1 {
				return nil, fmt.Errorf("invalid pane process")
			}
			kind := override
			if !knownKind(kind) {
				kind = Detect(command)
				if kind == "" {
					kind = descendantKind(pid, processes)
				}
			}
			if kind == "" {
				continue
			}
			server := &servers[current]
			membership := Membership{Session: fields[3], SessionName: display(name),
				Window: fields[2], WindowName: display(title)}
			found := false
			for i := range server.Agents {
				agent := &server.Agents[i]
				if agent.Pane != fields[1] {
					continue
				}
				found = true
				duplicate := false
				for _, member := range agent.Members {
					duplicate = duplicate || member.Session == membership.Session && member.Window == membership.Window
				}
				if !duplicate {
					agent.Members = append(agent.Members, membership)
				}
				break
			}
			if !found {
				ref := Reference{Version: Version, Host: host, Socket: server.Socket, Generation: server.Generation, Pane: fields[1]}
				server.Agents = append(server.Agents, Agent{ID: ref.ID(), Host: host, Socket: server.Socket,
					Generation: server.Generation, Pane: fields[1], Window: fields[2], Session: fields[3],
					Kind: kind, Path: display(directory), State: "unknown", Provenance: "unavailable",
					ObservedAt: now, Members: []Membership{membership}})
			}
		default:
			return nil, fmt.Errorf("unknown discovery record")
		}
	}
	sort.Slice(servers, func(i, j int) bool { return servers[i].Socket < servers[j].Socket })
	for i := range servers {
		sort.Slice(servers[i].Agents, func(a, b int) bool {
			left, _ := strconv.Atoi(servers[i].Agents[a].Pane[1:])
			right, _ := strconv.Atoi(servers[i].Agents[b].Pane[1:])
			return left < right
		})
	}
	return servers, nil
}

func (c Client) Capture(ctx context.Context, ids []string, lines int) (CaptureResponse, error) {
	if len(ids) < 1 || len(ids) > MaxTargets || lines < 1 || lines > MaxLines {
		return CaptureResponse{}, fmt.Errorf("capture needs 1-%d targets and 1-%d lines", MaxTargets, MaxLines)
	}
	response := CaptureResponse{Version: Version, Captures: make([]Capture, len(ids))}
	type group struct {
		host    config.Host
		refs    []Reference
		indices []int
	}
	groups := map[string]*group{}
	seen := map[string]bool{}
	for index, id := range ids {
		ref, err := ParseReference(id)
		if err != nil {
			return CaptureResponse{}, err
		}
		key := ref.ID()
		if seen[key] {
			return CaptureResponse{}, fmt.Errorf("duplicate capture target")
		}
		seen[key] = true
		host, ok := c.Config.Host(ref.Host)
		if !ok {
			return CaptureResponse{}, fmt.Errorf("agent host is not configured")
		}
		g := groups[host.ID]
		if g == nil {
			g = &group{host: host}
			groups[host.ID] = g
		}
		g.refs = append(g.refs, ref)
		g.indices = append(g.indices, index)
		response.Captures[index] = Capture{ID: id, Time: time.Now().UTC(), State: "unknown", Provenance: "unavailable"}
	}
	sem := make(chan struct{}, 4)
	var wg sync.WaitGroup
	for _, g := range groups {
		wg.Add(1)
		go func(g *group) {
			defer wg.Done()
			select {
			case sem <- struct{}{}:
				defer func() { <-sem }()
			case <-ctx.Done():
				for _, index := range g.indices {
					response.Captures[index].Error = commandFailure(execx.Result{Err: ctx.Err()})
				}
				return
			}
			result := c.run(ctx, g.host, captureScript(g.host, g.refs, lines))
			var captures []Capture
			var fail *Failure
			if result.Err != nil {
				fail = commandFailure(result)
			} else {
				var err error
				captures, err = parseCaptures(result.Stdout, g.refs)
				if err != nil {
					fail = failure("protocol", "invalid capture response")
				}
			}
			for i, index := range g.indices {
				if fail != nil {
					response.Captures[index].Error = fail
				} else {
					response.Captures[index] = captures[i]
					response.Captures[index].ID = ids[index]
				}
			}
		}(g)
	}
	wg.Wait()
	return response, nil
}

func parseCaptures(raw []byte, refs []Reference) ([]Capture, error) {
	records, err := protocol(raw)
	if err != nil {
		return nil, err
	}
	processes, err := parseProcesses(records)
	if err != nil {
		return nil, err
	}
	captures := make([]Capture, len(refs))
	seen := make([]bool, len(refs))
	for _, fields := range records {
		if fields[0] == "P" || fields[0] == "A" {
			continue
		}
		if len(fields) < 3 || (fields[0] != "C" && fields[0] != "F") {
			return nil, fmt.Errorf("invalid capture record")
		}
		index, err := strconv.Atoi(fields[1])
		if err != nil || index < 0 || index >= len(refs) || seen[index] {
			return nil, fmt.Errorf("invalid capture index")
		}
		seen[index] = true
		capture := Capture{ID: refs[index].ID(), Time: time.Now().UTC(), State: "unknown", Provenance: "unavailable"}
		if fields[0] == "F" {
			if len(fields) != 3 {
				return nil, fmt.Errorf("invalid capture failure")
			}
			switch fields[2] {
			case "5":
				capture.Error = failure("output_limit", "pane capture exceeded its limit")
			case "6":
				capture.Error = failure("unavailable", "tmux server unavailable")
			case "7":
				capture.Error = failure("protocol", "invalid tmux metadata")
			case "8":
				capture.Error = failure("stale", "tmux server changed; rediscover agents")
			case "9":
				capture.Error = failure("gone", "agent pane no longer available")
			default:
				capture.Error = failure("unavailable", "pane capture failed")
			}
		} else {
			if len(fields) != 7 || (fields[2] != "0" && fields[2] != "1") {
				return nil, fmt.Errorf("invalid capture fields")
			}
			pid, err := strconv.Atoi(fields[3])
			override, overrideErr := decoded(fields[4], 16384)
			command, commandErr := decoded(fields[5], 16384)
			text, textErr := decoded(fields[6], MaxBytes)
			if err != nil || pid < 1 || overrideErr != nil || commandErr != nil || textErr != nil {
				return nil, fmt.Errorf("invalid capture data")
			}
			kind := override
			if !knownKind(kind) {
				kind = Detect(command)
				if kind == "" {
					kind = descendantKind(pid, processes)
				}
			}
			if kind == "" || override == "off" {
				capture.Error = failure("gone", "pane no longer hosts a detected agent")
			} else {
				capture.Text, capture.Truncated = Clean(text), fields[2] == "1"
				if len(capture.Text) > MaxBytes {
					capture.Text = capture.Text[:MaxBytes]
					for !utf8.ValidString(capture.Text) {
						capture.Text = capture.Text[:len(capture.Text)-1]
					}
					capture.Truncated = true
				}
				capture.State, capture.Provenance = Classify(capture.Text), "heuristic"
			}
		}
		captures[index] = capture
	}
	for _, ok := range seen {
		if !ok {
			return nil, fmt.Errorf("missing capture result")
		}
	}
	return captures, nil
}

func (c Client) Resolve(ctx context.Context, id string) (config.Host, Reference, error) {
	ref, err := ParseReference(id)
	if err != nil {
		return config.Host{}, Reference{}, err
	}
	host, ok := c.Config.Host(ref.Host)
	if !ok {
		return config.Host{}, Reference{}, fmt.Errorf("agent host is not configured")
	}
	listing, err := c.List(ctx, host.ID)
	if err != nil {
		return config.Host{}, Reference{}, err
	}
	for _, result := range listing.Hosts {
		if result.Error != nil {
			return config.Host{}, Reference{}, result.Error
		}
		for _, server := range result.Servers {
			for _, agent := range server.Agents {
				if agent.ID == ref.ID() {
					return host, ref, nil
				}
			}
		}
	}
	return config.Host{}, Reference{}, failure("gone", "agent unavailable or server changed; rediscover agents")
}

func (c Client) TargetScript(host config.Host, ref Reference) (string, error) {
	configured, ok := c.Config.Host(ref.Host)
	if !ok || configured != host {
		return "", fmt.Errorf("agent host is not configured")
	}
	if err := ref.Validate(); err != nil {
		return "", err
	}
	return targetScript(host, ref), nil
}
