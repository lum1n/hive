package tmux

import (
	"os"
	"strings"

	"github.com/lum1n/hive/internal/sshx"
)

// Non-interactive SSH PATH is typically /usr/bin:/bin. tmux from Homebrew,
// MacPorts, Nix, conda, and the rest never shows up unless we add those dirs
// and then pick the same binary the live server is running.
const pathPrelude = `_saved_tmux=${TMUX:-}
unset TMUX
PATH="/usr/sbin:/sbin:/usr/bin:/bin:$PATH"
for _d in \
  /opt/homebrew/bin /opt/homebrew/opt/tmux/bin /opt/homebrew/sbin \
  /usr/local/bin /usr/local/opt/tmux/bin \
  /opt/local/bin /usr/pkg/bin /sw/bin \
  /home/linuxbrew/.linuxbrew/bin /home/linuxbrew/.linuxbrew/opt/tmux/bin \
  /snap/bin /run/current-system/sw/bin /nix/var/nix/profiles/default/bin /var/guix/profiles/per-user/"$USER"/current-guix/bin \
  "$HOME/.linuxbrew/bin" "$HOME/.linuxbrew/opt/tmux/bin" "$HOME/linuxbrew/.linuxbrew/bin" \
  "$HOME/.local/bin" "$HOME/bin" "$HOME/.nix-profile/bin" "$HOME/.guix-profile/bin" \
  "$HOME/.asdf/shims" "$HOME/.local/share/mise/shims" "$HOME/.cargo/bin" \
  "$HOME/miniconda3/bin" "$HOME/anaconda3/bin" "$HOME/miniforge3/bin" "$HOME/mambaforge/bin" "$HOME/.conda/bin"
do
[ -n "$_d" ] && [ -d "$_d" ] || continue
case ":$PATH:" in *":$_d:"*) ;; *) PATH="$_d:$PATH" ;; esac
done
[ -n "${HOMEBREW_PREFIX:-}" ] && [ -d "$HOMEBREW_PREFIX/bin" ] && case ":$PATH:" in *":$HOMEBREW_PREFIX/bin:"*) ;; *) PATH="$HOMEBREW_PREFIX/bin:$PATH" ;; esac
export PATH
`

const binPrelude = `HIVE_TMUX_BIN=""
if command -v lsof >/dev/null 2>&1; then
_user=$(id -un 2>/dev/null || printf %s "$USER")
HIVE_TMUX_BIN=$(lsof -nP -c tmux -a -u "$_user" -a -d txt -Fn 2>/dev/null | sed -n 's/^n//p' | grep '/tmux$' | head -n 1)
fi
if [ -z "$HIVE_TMUX_BIN" ]; then
_pid=$(pgrep -u "$(id -u)" -x tmux 2>/dev/null | head -n 1)
if [ -n "$_pid" ] && [ -r "/proc/$_pid/exe" ]; then
HIVE_TMUX_BIN=$(readlink "/proc/$_pid/exe" 2>/dev/null || true)
fi
fi
if [ -z "$HIVE_TMUX_BIN" ]; then
for _b in \
  /opt/homebrew/bin/tmux /opt/homebrew/opt/tmux/bin/tmux /opt/homebrew/Cellar/tmux/*/bin/tmux \
  /usr/local/bin/tmux /usr/local/opt/tmux/bin/tmux /usr/local/Cellar/tmux/*/bin/tmux \
  /opt/local/bin/tmux /usr/pkg/bin/tmux /sw/bin/tmux \
  /home/linuxbrew/.linuxbrew/bin/tmux /home/linuxbrew/.linuxbrew/opt/tmux/bin/tmux /home/linuxbrew/.linuxbrew/Cellar/tmux/*/bin/tmux \
  /snap/bin/tmux /run/current-system/sw/bin/tmux /nix/var/nix/profiles/default/bin/tmux \
  "$HOME/.linuxbrew/bin/tmux" "$HOME/.linuxbrew/opt/tmux/bin/tmux" $HOME/.linuxbrew/Cellar/tmux/*/bin/tmux \
  "$HOME/linuxbrew/.linuxbrew/bin/tmux" "$HOME/.local/bin/tmux" "$HOME/bin/tmux" \
  "$HOME/.nix-profile/bin/tmux" "$HOME/.guix-profile/bin/tmux" \
  "$HOME/.asdf/shims/tmux" "$HOME/.local/share/mise/shims/tmux" \
  "$HOME/miniconda3/bin/tmux" "$HOME/anaconda3/bin/tmux" "$HOME/miniforge3/bin/tmux" "$HOME/mambaforge/bin/tmux" \
  /usr/bin/tmux /bin/tmux
do
[ -n "$_b" ] && [ -x "$_b" ] || continue
HIVE_TMUX_BIN="$_b"
break
done
fi
if [ -z "$HIVE_TMUX_BIN" ]; then
HIVE_TMUX_BIN=$(command -v tmux 2>/dev/null || true)
fi
[ -n "$HIVE_TMUX_BIN" ] || HIVE_TMUX_BIN=tmux
`

// Cmd is argv to run a tmux subcommand on a host.
// Over SSH, and locally when not already inside tmux, this wraps PATH and
// socket discovery so macOS GUI/Terminal sessions are visible.
func Cmd(local bool, bin, socket string, rest ...string) []string {
	if local && socket == "" && os.Getenv("TMUX") != "" {
		return Args(bin, "", rest...)
	}
	return []string{"sh", "-c", Script(bin, socket, rest...)}
}

func AttachCmd(local bool, bin, socket, name string) []string {
	if local && socket == "" && os.Getenv("TMUX") != "" {
		return AttachCommand(bin, socket, name)
	}
	return []string{"sh", "-c", AttachScript(bin, socket, name)}
}

func Script(bin, socket string, rest ...string) string {
	return Prelude(bin, socket) + "hive_tmux " + quoteArgs(rest) + "\n"
}

func AttachScript(bin, socket, name string) string {
	target := sshx.SingleQuote(exact(name))
	return Prelude(bin, socket) +
		"hive_tmux set-option -t " + target + " window-size latest 2>/dev/null; " +
		"hive_tmux set-option -t " + target + " destroy-unattached off 2>/dev/null; " +
		"hive_tmux_exec -u attach-session -t " + target + "\n"
}

func Prelude(bin, socket string) string {
	var b strings.Builder
	b.WriteString(pathPrelude)
	if strings.Contains(bin, "/") {
		b.WriteString("HIVE_TMUX_BIN=" + sshx.SingleQuote(bin) + "\n")
	} else {
		b.WriteString(binPrelude)
	}
	if socket != "" {
		if strings.Contains(socket, "/") {
			b.WriteString("HIVE_TMUX_SOCK=" + sshx.SingleQuote(socket) + "\n")
			b.WriteString("HIVE_TMUX_L=\n")
			b.WriteString("HIVE_TMUX_SOCKS=\n")
		} else {
			b.WriteString("HIVE_TMUX_L=" + sshx.SingleQuote(socket) + "\n")
			b.WriteString("HIVE_TMUX_SOCK=\n")
			b.WriteString("HIVE_TMUX_SOCKS=\n")
		}
	} else {
		b.WriteString("HIVE_TMUX_L=\n")
		b.WriteString("HIVE_TMUX_SOCK=\n")
		b.WriteString(socketPrelude)
	}
	b.WriteString(tmuxFn)
	return b.String()
}

const socketPrelude = `uid=$(id -u)
_user=$(id -un 2>/dev/null || printf %s "$USER")
HIVE_TMUX_SOCKS=""
hive_add_sock() {
_sock=$1
[ -n "$_sock" ] && [ -S "$_sock" ] || return 0
case "
$HIVE_TMUX_SOCKS
" in *"
$_sock
"*) return 0 ;; esac
if [ -n "$HIVE_TMUX_SOCKS" ]; then
HIVE_TMUX_SOCKS="$HIVE_TMUX_SOCKS
$_sock"
else
HIVE_TMUX_SOCKS="$_sock"
fi
}
if command -v lsof >/dev/null 2>&1; then
while IFS= read -r _sock; do
hive_add_sock "$_sock"
done <<EOF
$(lsof -nP -c tmux -a -u "$_user" -U -Fn 2>/dev/null | sed -n 's/^n//p' | sed 's/ (.*//' | grep tmux-)
EOF
fi
hive_add_sock "${_saved_tmux%%,*}"
hive_add_sock "${TMUX_TMPDIR:+$TMUX_TMPDIR/tmux-$uid/default}"
hive_add_sock "${TMPDIR:+${TMPDIR%/}/tmux-$uid/default}"
for _sock in /var/folders/*/*/T/tmux-$uid/* /private/tmp/tmux-$uid/* "$HOME/.tmux/tmp/tmux-$uid/"* /tmp/tmux-$uid/* /private/tmp/tmux-*/default /tmp/tmux-*/default; do
hive_add_sock "$_sock"
done
`

const tmuxFn = `hive_tmux() {
if [ -n "$HIVE_TMUX_SOCK" ]; then
"$HIVE_TMUX_BIN" -S "$HIVE_TMUX_SOCK" "$@"
return $?
fi
if [ -n "$HIVE_TMUX_L" ]; then
"$HIVE_TMUX_BIN" -L "$HIVE_TMUX_L" "$@"
return $?
fi
if [ -n "$HIVE_TMUX_SOCKS" ]; then
_ec=1
_listed=0
while IFS= read -r _sock; do
[ -n "$_sock" ] || continue
if [ "$1" = "list-sessions" ]; then
if _out=$("$HIVE_TMUX_BIN" -S "$_sock" "$@" 2>/dev/null); then
printf '%s\n' "$_out"
_listed=1
_ec=0
fi
else
"$HIVE_TMUX_BIN" -S "$_sock" "$@"
_ec=$?
[ "$_ec" -eq 0 ] && return 0
fi
done <<SOCKS
$HIVE_TMUX_SOCKS
SOCKS
[ "$_listed" -eq 1 ] && return 0
return $_ec
fi
"$HIVE_TMUX_BIN" "$@"
}
hive_tmux_exec() {
if [ -n "$HIVE_TMUX_SOCK" ]; then
exec "$HIVE_TMUX_BIN" -S "$HIVE_TMUX_SOCK" "$@"
fi
if [ -n "$HIVE_TMUX_L" ]; then
exec "$HIVE_TMUX_BIN" -L "$HIVE_TMUX_L" "$@"
fi
_t=""
_prev=""
for _a in "$@"; do
if [ "$_prev" = "-t" ]; then
_t=$_a
break
fi
_prev=$_a
done
_sock=""
if [ -n "$HIVE_TMUX_SOCKS" ] && [ -n "$_t" ]; then
while IFS= read -r _s; do
[ -n "$_s" ] || continue
if "$HIVE_TMUX_BIN" -S "$_s" has-session -t "$_t" </dev/null 2>/dev/null; then
_sock=$_s
break
fi
done <<SOCKS
$HIVE_TMUX_SOCKS
SOCKS
fi
if [ -z "$_sock" ] && [ -n "$HIVE_TMUX_SOCKS" ]; then
_sock=$(printf '%s\n' "$HIVE_TMUX_SOCKS" | head -n 1)
fi
if [ -n "$_sock" ]; then
exec "$HIVE_TMUX_BIN" -S "$_sock" "$@"
fi
exec "$HIVE_TMUX_BIN" "$@"
}
`

func quoteArgs(args []string) string {
	parts := make([]string, len(args))
	for i, a := range args {
		parts[i] = sshx.SingleQuote(a)
	}
	return strings.Join(parts, " ")
}
