package agents

import (
	"fmt"
	"strings"

	"github.com/lum1n/hive/internal/config"
	"github.com/lum1n/hive/internal/sshx"
	"github.com/lum1n/hive/internal/tmux"
)

const probeHelpers = `
hive_b64() { base64 | tr -d '\r\n'; }
hive_canonical() {
_directory=$(dirname "$1")
_file=$(basename "$1")
(CDPATH= cd -- "$_directory" && printf '%s/%s' "$(pwd -P)" "$_file")
}
hive_field() {
_value=$("$HIVE_TMUX_BIN" -S "$_sock" display-message -p -t "$_target" "$1" 2>/dev/null) || return 1
printf %s "$_value" | hive_b64
}
hive_sockets() {
if [ -n "$HIVE_TMUX_SOCK" ]; then
printf '%s\n' "$HIVE_TMUX_SOCK"
elif [ -n "$HIVE_TMUX_L" ]; then
"$HIVE_TMUX_BIN" -L "$HIVE_TMUX_L" display-message -p '#{socket_path}' 2>/dev/null
elif [ -n "$HIVE_TMUX_SOCKS" ]; then
printf '%s\n' "$HIVE_TMUX_SOCKS"
else
"$HIVE_TMUX_BIN" display-message -p '#{socket_path}' 2>/dev/null
fi
}
_sockets=$(hive_sockets)
command -v "$HIVE_TMUX_BIN" >/dev/null 2>&1 || exit 6
for _utility in base64 tr ps id awk head tail dirname basename; do
command -v "$_utility" >/dev/null 2>&1 || exit 4
done
_pinned=0
[ -n "$HIVE_TMUX_SOCK$HIVE_TMUX_L" ] && _pinned=1
`

const processProbe = `
_processes=$(ps -u "$(id -u)" -o pid=,ppid=,comm=) || exit 4
printf 'P\t'; printf %s "$_processes" | hive_b64; printf '\n'
`

const wrapperProbe = `
_wrappers=$(printf '%s\n' "$_processes" | awk -v roots="$_roots" '
BEGIN { count=split(roots, values, " "); for(i=1;i<=count;i++) root[values[i]]=1 }
{ parent[$1]=$2; n=$0; sub(/^[[:space:]]*[0-9]+[[:space:]]+[0-9]+[[:space:]]+/,"",n); sub(/^.*\//,"",n); if(n=="node" || n=="bun" || n=="deno") wrapper[$1]=1 }
END {
for(pid in wrapper) {
p=pid
for(i=0;i<1024 && p;i++) {
if(p in root) { print pid; break }
p=parent[p]
}
}
}') || exit 4
_count=0
while IFS= read -r _pid; do
[ -n "$_pid" ] || continue
_count=$((_count + 1))
[ "$_count" -le 128 ] || exit 5
_args=$(ps -ww -p "$_pid" -o args= 2>/dev/null) || continue
[ "${#_args}" -le 8192 ] || exit 5
printf 'A\t%s\t' "$_pid"; printf %s "$_args" | hive_b64; printf '\n'
done <<WRAPPERS
$_wrappers
WRAPPERS
`

func inventoryScript(host config.Host) string {
	return tmux.Prelude(host.TmuxBin(), host.Socket) + probeHelpers + `
printf 'V\t1\n'
` + processProbe + `
if [ -z "$_sockets" ] && [ "$_pinned" -eq 1 ]; then exit 6; fi
_server_count=0
_pane_count=0
_roots=""
while IFS= read -r _sock; do
[ -n "$_sock" ] || continue
_server_count=$((_server_count + 1))
[ "$_server_count" -le 64 ] || exit 5
if ! [ -S "$_sock" ] || ! [ -O "$_sock" ]; then
if [ "$_pinned" -eq 1 ]; then exit 6; fi
continue
fi
_server=$("$HIVE_TMUX_BIN" -S "$_sock" display-message -p '#{socket_path}' 2>/dev/null) || {
printf 'E\t'; printf %s "$_sock" | hive_b64; printf '\tunavailable\n'
continue
}
_server=$(hive_canonical "$_server") || exit 6
_sock=$_server
_generation=$("$HIVE_TMUX_BIN" -S "$_sock" display-message -p '#{pid}:#{start_time}' 2>/dev/null) || exit 6
printf 'S\t'; printf %s "$_server" | hive_b64; printf '\t%s\n' "$_generation"
_rows=$("$HIVE_TMUX_BIN" -S "$_sock" list-panes -a -F '#{pane_id} #{window_id} #{session_id} #{pane_pid} #{pane_active} #{pane_dead} #{?#{@agent-overview-owned},1,0}' 2>/dev/null) || exit 6
while read -r _pane _window _session _pid _active _dead _owned _extra; do
[ -n "$_pane" ] || continue
[ -z "$_extra" ] || exit 7
_pane_count=$((_pane_count + 1))
[ "$_pane_count" -le 4096 ] || exit 5
_target="$_session:$_window.$_pane"
_kind=$(hive_field '#{@agent-overview-kind}') || exit 6
if [ "$_owned" = 0 ] && [ "$_dead" = 0 ] && [ "$_kind" != b2Zm ]; then _roots="$_roots $_pid"; fi
_command=$(hive_field '#{pane_current_command}') || exit 6
_path=$(hive_field '#{pane_current_path}') || exit 6
_name=$(hive_field '#{session_name}') || exit 6
_title=$(hive_field '#{window_name}') || exit 6
printf 'N\t%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\n' "$_pane" "$_window" "$_session" "$_pid" "$_active" "$_dead" "$_owned" "$_kind" "$_command" "$_path" "$_name" "$_title"
done <<PANES
$_rows
PANES
_after=$("$HIVE_TMUX_BIN" -S "$_sock" display-message -p '#{pid}:#{start_time}' 2>/dev/null) || exit 6
[ "$_after" = "$_generation" ] || exit 8
done <<SOCKETS
$_sockets
SOCKETS
` + wrapperProbe
}

func invocation(opt sshx.Options, host config.Host, script string) (string, []string) {
	command := []string{"sh", "-c", script}
	if host.Local {
		return command[0], command[1:]
	}
	return "ssh", sshx.ExecArgs(opt, host.ID, host.Destination(), host.ControlPath, command)
}

func targetScript(host config.Host, ref Reference) string {
	return tmux.Prelude(host.TmuxBin(), host.Socket) + probeHelpers + targetGuard(ref)
}

func targetGuard(ref Reference) string {
	return `
_sock=` + sshx.SingleQuote(ref.Socket) + `
_allowed=0
while IFS= read -r _candidate; do
[ -n "$_candidate" ] || continue
[ -S "$_candidate" ] && [ -O "$_candidate" ] || continue
_resolved=$("$HIVE_TMUX_BIN" -S "$_candidate" display-message -p '#{socket_path}' 2>/dev/null) || continue
_resolved=$(hive_canonical "$_resolved") || continue
if [ "$_resolved" = "$_sock" ]; then _allowed=1; break; fi
done <<CANDIDATES
$_sockets
CANDIDATES
[ "$_allowed" -eq 1 ] || exit 6
` + generationGuard(ref) + `
_target=` + sshx.SingleQuote(ref.Pane) + `
_identity=$("$HIVE_TMUX_BIN" -S "$_sock" display-message -p -t "$_target" '#{pane_id} #{window_id} #{session_id} #{pane_height} #{pane_width} #{pane_dead} #{?#{@agent-overview-owned},1,0} #{pane_pid}' 2>/dev/null) || exit 9
set -- $_identity
[ "$#" -eq 8 ] && [ "$1" = "$_target" ] && [ "$6" = 0 ] && [ "$7" = 0 ] || exit 9
_window=$2
_session=$3
_height=$4
_width=$5
_pid=$8
[ "$_window" != "${_window#@}" ] && [ "$_session" != "${_session#\$}" ] || exit 7
[ "${_window#@}" -ge 0 ] 2>/dev/null && [ "${_session#\$}" -ge 0 ] 2>/dev/null && [ "$_pid" -gt 0 ] 2>/dev/null || exit 7
_kind=$(hive_field '#{@agent-overview-kind}') || exit 9
[ "$_kind" != b2Zm ] || exit 9
`
}

func generationGuard(ref Reference) string {
	return `
_generation=$("$HIVE_TMUX_BIN" -S "$_sock" display-message -p '#{pid}:#{start_time}' 2>/dev/null) || exit 6
[ "$_generation" = ` + sshx.SingleQuote(ref.Generation) + ` ] || exit 8
`
}

func captureScript(host config.Host, refs []Reference, lines int) string {
	var b strings.Builder
	b.WriteString(tmux.Prelude(host.TmuxBin(), host.Socket) + probeHelpers)
	b.WriteString("printf 'V\\t1\\n'\n")
	b.WriteString(processProbe)
	b.WriteString("_roots=\"\"\n")
	for _, ref := range refs {
		b.WriteString("_root=$(\n" + targetGuard(ref) + "\nprintf %s \"$_pid\"\n) || _root=\"\"\n")
		b.WriteString("_roots=\"$_roots $_root\"\n")
	}
	b.WriteString(wrapperProbe)
	for index, ref := range refs {
		b.WriteString("(\n")
		b.WriteString(targetGuard(ref))
		b.WriteString("_command=$(hive_field '#{pane_current_command}') || exit 9\n")
		b.WriteString(fmt.Sprintf(`
case "$_height:$_width" in *[!0-9:]*|:*|*:) exit 7 ;; esac
[ "$_width" -le 4096 ] && [ "$_height" -ge 1 ] && [ "$_height" -le 512 ] || exit 5
_end=$((_height - 1))
_start=-%d
_text=$("$HIVE_TMUX_BIN" -S "$_sock" capture-pane -p -S "$_start" -E "$_end" -t "$_target" 2>/dev/null) || exit 9
_text=$(printf %%s "$_text" | tail -n %d)
`, lines, lines))
		b.WriteString(generationGuard(ref))
		b.WriteString(fmt.Sprintf(`
_truncated=0
[ "${#_text}" -le %d ] || _truncated=1
printf 'C\t%d\t%%s\t%%s\t%%s\t%%s\t' "$_truncated" "$_pid" "$_kind" "$_command"
printf %%s "$_text" | head -c %d | hive_b64
printf '\n'
) || printf 'F\t%d\t%%s\n' "$?"
`, MaxBytes, index, MaxBytes, index))
	}
	return "LC_ALL=C\nexport LC_ALL\n" + b.String()
}
