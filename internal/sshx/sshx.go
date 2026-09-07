package sshx

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"os"
	"os/user"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"
)

type Options struct {
	RuntimeDir     string
	ControlPersist time.Duration
}

func (o Options) persistSeconds() int {
	if o.ControlPersist <= 0 {
		return 120
	}
	return int(o.ControlPersist.Seconds())
}

func RuntimeDir() (string, error) {
	base := os.Getenv("XDG_RUNTIME_DIR")
	if base == "" {
		uid := os.Getuid()
		base = filepath.Join("/tmp", "hive-"+strconv.Itoa(uid))
	} else {
		base = filepath.Join(base, "hive")
	}
	if err := os.MkdirAll(base, 0o700); err != nil {
		return "", err
	}
	info, err := os.Lstat(base)
	if err != nil {
		return "", err
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return "", fmt.Errorf("unsafe runtime directory %s (symlink)", base)
	}
	if stat, ok := info.Sys().(*syscall.Stat_t); ok && int(stat.Uid) != os.Getuid() {
		return "", fmt.Errorf("unsafe runtime directory %s", base)
	}
	if err := os.Chmod(base, 0o700); err != nil {
		return "", err
	}
	return base, nil
}

func ControlPath(runtimeDir, hostID string) string {
	sum := sha256.Sum256([]byte(hostID))
	return filepath.Join(runtimeDir, hex.EncodeToString(sum[:8])+".ssh")
}

func baseSSH(batch bool) []string {
	args := []string{
		"-o", "ConnectTimeout=5",
		"-o", "ServerAliveInterval=15",
		"-o", "ServerAliveCountMax=2",
	}
	if batch {
		args = append(args, "-o", "BatchMode=yes")
	}
	return args
}

func muxArgs(opt Options, hostID, controlPath string) []string {
	if controlPath != "" {
		return []string{
			"-S", expandHome(controlPath),
			"-o", "ProxyCommand=false",
		}
	}
	return []string{
		"-o", "ControlMaster=auto",
		"-o", "ControlPersist=" + strconv.Itoa(opt.persistSeconds()),
		"-o", "ControlPath=" + ControlPath(opt.RuntimeDir, hostID),
	}
}

func attachMuxArgs(opt Options, hostID, controlPath string) []string {
	if controlPath != "" {
		return muxArgs(opt, hostID, controlPath)
	}
	// Discovery opens a no-TTY ControlMaster. A slave on that socket cannot
	// allocate a PTY, so attach uses a fresh connection.
	return []string{
		"-o", "ControlMaster=no",
		"-o", "ControlPath=none",
		"-o", "RequestTTY=force",
	}
}

// ExecArgs builds `ssh -T … dest -- remote`.
func ExecArgs(opt Options, hostID, dest, controlPath string, remote []string) []string {
	args := []string{"-T"}
	args = append(args, baseSSH(true)...)
	args = append(args, muxArgs(opt, hostID, controlPath)...)
	args = append(args, dest, "--", remoteCommand(remote))
	return args
}

// AttachArgs builds `ssh -t … dest -- remote`.
func AttachArgs(opt Options, hostID, dest, controlPath string, remote []string) []string {
	args := []string{"-tt"}
	args = append(args, baseSSH(true)...)
	args = append(args, attachMuxArgs(opt, hostID, controlPath)...)
	args = append(args, dest, "--", remoteCommand(remote))
	return args
}

func remoteCommand(args []string) string {
	if len(args) >= 3 && args[0] == "sh" && args[1] == "-c" {
		return decodePipe(args[2])
	}
	parts := make([]string, len(args))
	for i, a := range args {
		parts[i] = SingleQuote(a)
	}
	return strings.Join(parts, " ")
}

func decodePipe(script string) string {
	enc := base64.StdEncoding.EncodeToString([]byte(script))
	return "f=$(mktemp 2>/dev/null || mktemp -t hive) && printf '%s\\n' " + SingleQuote(enc) +
		" | { base64 -d 2>/dev/null || base64 -D; } >\"$f\" && sh \"$f\"; e=$?; rm -f \"$f\"; exit $e"
}

func SingleQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'"'"'`) + "'"
}

func expandHome(path string) string {
	if strings.HasPrefix(path, "~/") {
		if u, err := user.Current(); err == nil {
			return filepath.Join(u.HomeDir, path[2:])
		}
	}
	return os.ExpandEnv(path)
}
