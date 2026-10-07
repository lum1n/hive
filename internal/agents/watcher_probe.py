import base64
import json
import os
import re
import selectors
import socket
import stat
import struct
import subprocess
import sys
import time

KINDS = ("copilot", "claude", "codex", "pi", "opencode", "cursor")
STATES = ("unknown", "idle", "thinking", "running-tool", "waiting-permission", "errored")
LIMIT = 1024 * 1024


class ProbeError(Exception):
    def __init__(self, code):
        self.code = code


def tmux_output(arguments, deadline):
    with subprocess.Popen(arguments, stdout=subprocess.PIPE, stderr=subprocess.DEVNULL) as child:
        try:
            data = bytearray()
            with selectors.DefaultSelector() as selector:
                selector.register(child.stdout, selectors.EVENT_READ)
                while selector.get_map():
                    remaining = deadline - time.monotonic()
                    if remaining <= 0:
                        raise ProbeError("timeout")
                    for key, _ in selector.select(remaining):
                        chunk = os.read(key.fd, 65536)
                        if not chunk:
                            selector.unregister(key.fileobj)
                        data.extend(chunk)
                        if len(data) > LIMIT:
                            raise ProbeError("output_limit")
            if child.wait(timeout=max(.01, deadline - time.monotonic())):
                raise ProbeError("unavailable")
            return bytes(data).rstrip(b"\n")
        finally:
            if child.poll() is None:
                child.kill()
            child.wait()


def snapshot(path, deadline):
    if not os.path.isabs(path) or os.path.normpath(path) != path or any(c in path for c in "\0\r\n"):
        raise ProbeError("unavailable")
    parent = os.path.dirname(os.path.realpath(path))
    while True:
        directory = os.stat(parent)
        if (not stat.S_ISDIR(directory.st_mode) or directory.st_uid not in (0, os.getuid()) or
                (directory.st_mode & 0o022 and not directory.st_mode & stat.S_ISVTX)):
            raise ProbeError("unavailable")
        if parent == "/":
            break
        parent = os.path.dirname(parent)
    info = os.lstat(path)
    if not stat.S_ISSOCK(info.st_mode) or info.st_uid != os.getuid() or info.st_mode & 0o077:
        raise ProbeError("unavailable")
    with socket.socket(socket.AF_UNIX) as connection:
        connection.settimeout(max(.01, deadline - time.monotonic()))
        connection.connect(path)
        current = os.lstat(path)
        if (current.st_dev, current.st_ino, current.st_uid, current.st_mode) != (
                info.st_dev, info.st_ino, info.st_uid, info.st_mode):
            raise ProbeError("unavailable")
        if hasattr(socket, "SO_PEERCRED"):
            credentials = connection.getsockopt(socket.SOL_SOCKET, socket.SO_PEERCRED, struct.calcsize("3i"))
            if struct.unpack("3i", credentials)[1] != os.getuid():
                raise ProbeError("unavailable")
        connection.sendall(b'{"cmd":"snapshot"}\n')
        buffer, total, events, quotas = b"", 0, 0, {}
        while events < 32:
            remaining = deadline - time.monotonic()
            if remaining <= 0:
                raise ProbeError("timeout")
            connection.settimeout(remaining)
            chunk = connection.recv(65536)
            if not chunk:
                raise ProbeError("unavailable")
            buffer += chunk
            total += len(chunk)
            if total > LIMIT:
                raise ProbeError("output_limit")
            while b"\n" in buffer:
                line, buffer = buffer.split(b"\n", 1)
                event = json.loads(line)
                events += 1
                if events > 32:
                    raise ProbeError("output_limit")
                if not isinstance(event, dict) or type(event.get("v")) is not int or event["v"] != 1:
                    raise ProbeError("protocol")
                if event.get("type") == "snapshot":
                    return event.get("agents"), list(quotas.values())
                if event.get("type") == "quota":
                    reading = quota(event)
                    if reading:
                        quotas[reading["kind"]] = reading
                # hello, live state/gone frames, and newer event types precede
                # our snapshot on the shared stream; they are not failures.
        raise ProbeError("output_limit")


def quota(event):
    """Normalized subscription usage, or None. Optional: never fails the probe."""
    kind, plan, windows = event.get("kind"), event.get("plan", ""), event.get("windows")
    if (kind not in KINDS or not isinstance(plan, str) or len(plan) > 64 or
            not isinstance(windows, list) or not 0 < len(windows) <= 8):
        return None
    result = []
    for window in windows:
        if not isinstance(window, dict):
            return None
        label, used, resets = window.get("label"), window.get("usedPercent"), window.get("resetsAt")
        if (not isinstance(label, str) or not 0 < len(label) <= 32 or
                type(used) not in (int, float) or not 0 <= used <= 100 or
                not (resets is None or (isinstance(resets, str) and len(resets) <= 64))):
            return None
        row = {"label": label, "used_percent": used}
        if resets:
            row["resets_at"] = resets
        result.append(row)
    return {"kind": kind, "plan": plan, "stale": event.get("stale") is True, "windows": result}


def records(rows):
    if not isinstance(rows, list) or len(rows) > 4096:
        raise ProbeError("protocol")
    result = {}
    for row in rows:
        if not isinstance(row, dict):
            raise ProbeError("protocol")
        name, index, kind = (row.get(key) for key in ("session", "window", "kind"))
        state = row.get("state", "unknown")
        if (not isinstance(name, str) or len(name.encode("utf-8")) > 16384 or
                type(index) is not int or index < 0 or kind not in KINDS or state not in STATES or
                type(row.get("unbound", False)) is not bool):
            raise ProbeError("protocol")
        key = (name, index)
        if key in result:
            raise ProbeError("protocol")
        result[key] = None if row.get("unbound", False) else (kind, state)
    return result


def panes(data):
    result = []
    while data:
        if len(result) >= 4096:
            raise ProbeError("output_limit")
        header, separator, data = data.partition(b"\t")
        fields = header.decode("ascii").split()
        if not separator or len(fields) != 6:
            raise ProbeError("protocol")
        pane, window, index, active, pid, owned = fields
        if (not re.fullmatch(r"%[0-9]+", pane) or not re.fullmatch(r"@[0-9]+", window) or
                not index.isdigit() or active not in ("0", "1") or owned not in ("0", "1") or
                not pid.isdigit() or int(pid) < 1):
            raise ProbeError("protocol")
        size, separator, data = data.partition(b":")
        if not separator or not size.isdigit() or len(size) > 5:
            raise ProbeError("protocol")
        size = int(size)
        if size > 16384 or len(data) <= size or data[size:size+1] != b",":
            raise ProbeError("protocol")
        name, data = data[:size].decode("utf-8"), data[size+1:]
        if data:
            if not data.startswith(b"\n"):
                raise ProbeError("protocol")
            data = data[1:]
        result.append((pane, window, int(index), active, int(pid), owned, name))
    return result


def probe(path, binary, server, generation):
    deadline = time.monotonic() + .8
    command = [binary, "-u", "-S", server]
    if tmux_output(command + ["display-message", "-p", "#{pid}:#{start_time}"], deadline).decode("ascii") != generation:
        raise ProbeError("stale")
    rows, quotas = snapshot(path, deadline)
    rows = records(rows)
    fmt = ("#{pane_id} #{window_id} #{window_index} #{pane_active} #{pane_pid} "
           "#{?#{@agent-overview-owned},1,0}\t#{n:session_name}:#{session_name},")
    metadata = panes(tmux_output(command + ["list-panes", "-a", "-F", fmt], deadline))
    windows, matches, conflicts = {}, {}, set()
    for pane, window, *_ in metadata:
        windows.setdefault(window, set()).add(pane)
    for pane, window, index, active, pid, owned, name in metadata:
        row = rows.get((name, index))
        if active != "1" or owned != "0" or len(windows[window]) != 1 or row is None:
            continue
        kind, state = row
        match = {"pane": pane, "window": window, "pid": pid, "kind": kind, "state": state}
        if pane in matches and matches[pane] != match:
            conflicts.add(pane)
        matches[pane] = match
    if tmux_output(command + ["display-message", "-p", "#{pid}:#{start_time}"], deadline).decode("ascii") != generation:
        raise ProbeError("stale")
    return {"status": "ready", "states": [row for pane, row in matches.items() if pane not in conflicts],
            "quota": quotas}


def main():
    try:
        result = probe(*sys.argv[1:])
    except ProbeError as error:
        result = {"status": "unavailable", "states": [], "error": error.code}
    except (socket.timeout, subprocess.TimeoutExpired):
        result = {"status": "unavailable", "states": [], "error": "timeout"}
    except OSError:
        result = {"status": "unavailable", "states": [], "error": "unavailable"}
    except (ValueError, UnicodeError, RecursionError):
        result = {"status": "unavailable", "states": [], "error": "protocol"}
    encoded = base64.b64encode(json.dumps(result, separators=(",", ":")).encode()).decode()
    print("W\t" + encoded)


if __name__ == "__main__":
    main()
