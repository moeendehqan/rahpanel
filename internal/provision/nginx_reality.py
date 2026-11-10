"""Share the stock Ubuntu Nginx HTTPS listeners with one RAH node."""

import fcntl
import fnmatch
import os
from pathlib import Path
import re
import shutil
import socket
import ssl
import subprocess
import sys
import tempfile
import time


MODE, SNI, REALITY_PORT, SITE_PORT = sys.argv[1:]
ROOT = Path("/etc/nginx")
MAIN = ROOT / "nginx.conf"
BACKUP = Path("/etc/rahpanel/nginx-before-reality")
MARKER = "# RAH managed REALITY TCP 443"
TOKEN = re.compile(r"""\s+|\#[^\n]*|[{};]|"(?:(?:\\.)|[^"\\])*"|'(?:(?:\\.)|[^'\\])*'|(?:\\.|\$\{[^}]*\}|[^\s{};'"\\])+""")


def run(*args):
    return subprocess.check_output(args, stderr=subprocess.STDOUT, text=True)


def value(token):
    return token.group().strip("'\"")


def plan():
    pid = int(run("systemctl", "show", "nginx.service", "-p", "MainPID", "--value"))
    if not pid:
        raise ValueError("nginx.service must be running")
    title = Path(f"/proc/{pid}/cmdline").read_bytes().replace(b"\0", b" ")
    if not title.startswith(b"nginx: master process /usr/sbin/nginx") or re.search(rb"\s-[cp]", title):
        raise ValueError("automatic sharing requires the standard Ubuntu nginx.service configuration")
    dump = run("/usr/sbin/nginx", "-T")
    files = list(dict.fromkeys(re.findall(r"^# configuration file (.+):$", dump, re.M)))
    if str(MAIN) not in files:
        raise ValueError("could not identify the active nginx.conf")
    if MAIN.is_symlink():
        raise ValueError("automatic sharing requires a regular nginx.conf")

    main = MAIN.read_text()
    if MARKER in main:
        if not BACKUP.is_dir() or f'        "{SNI}" 127.0.0.1:{REALITY_PORT};' not in main:
            raise ValueError("existing RAH stream settings do not match this node")
        return {}, []

    changes = {}
    families = set()
    for name in files:
        path = Path(name).resolve()
        source = path.read_bytes().decode("utf-8", errors="surrogateescape")
        args = []
        edits = []
        for token in TOKEN.finditer(source):
            raw = token.group()
            if raw.isspace() or raw.startswith("#"):
                continue
            if raw not in (";", "{", "}"):
                args.append(token)
                continue
            words = list(map(value, args))
            if words and words[0] == "stream" and raw == "{":
                raise ValueError("existing stream configuration requires manual routing")
            if words and words[0] == "server_name":
                for name_pattern in words[1:]:
                    name_pattern = name_pattern.lower().rstrip(".")
                    if name_pattern.startswith("~") or fnmatch.fnmatchcase(SNI.lower(), name_pattern):
                        raise ValueError("REALITY SNI may overlap an existing Nginx server name")
                    if name_pattern.startswith(".") and (SNI.lower() == name_pattern[1:] or SNI.lower().endswith(name_pattern)):
                        raise ValueError("REALITY SNI overlaps an existing Nginx server name")
            if len(words) > 1 and words[0] == "listen" and (words[1] == "443" or words[1].endswith(":443")):
                address, *options = words[1:]
                if address not in ("443", "*:443", "0.0.0.0:443", "[::]:443") or "ssl" not in options or set(options) - {"ssl", "default_server", "http2"}:
                    raise ValueError("automatic sharing requires standard wildcard TLS listeners on TCP 443")
                family = "::1" if address == "[::]:443" else "127.0.0.1"
                families.add(family)
                endpoint = "[::1]" if family == "::1" else family
                edits.append((args[1].start(), args[1].end(), f"{endpoint}:{SITE_PORT}"))
            args = []
        if edits:
            if not path.is_relative_to(ROOT):
                raise ValueError("a TCP 443 listener is configured outside /etc/nginx")
            for start, end, replacement in reversed(edits):
                source = source[:start] + replacement + source[end:]
            changes[path] = source

    if not families:
        raise ValueError("no supported HTTPS listener was found on TCP 443")
    if run("ss", "-H", "-ltn", f"sport = :{SITE_PORT}").strip():
        raise ValueError(f"local HTTPS port {SITE_PORT} is already in use")

    # Use separate stream servers so each address family keeps its own HTTPS
    # virtual hosts and default certificate.
    stream = [MARKER, "stream {"]
    for address in sorted(families):
        ipv6 = address == "::1"
        name = "rah_tls_v6" if ipv6 else "rah_tls_v4"
        local = "[::1]" if ipv6 else address
        public = "[::]:443" if ipv6 else "443"
        stream += [
            f"    map $ssl_preread_server_name ${name} {{",
            f'        "{SNI}" 127.0.0.1:{REALITY_PORT};',
            f"        default {local}:{SITE_PORT};",
            "    }",
            "    server {",
            f"        listen {public};",
            "        ssl_preread on;",
            "        proxy_connect_timeout 5s;",
            "        proxy_timeout 1h;",
            f"        proxy_pass ${name};",
            "    }",
        ]
    changes[MAIN] = changes.pop(MAIN, main) + "\n" + "\n".join(stream + ["}", ""])
    return changes, sorted(families)


def certificate(address):
    context = ssl._create_unverified_context()
    with socket.create_connection((address, 443), timeout=3) as sock:
        with context.wrap_socket(sock) as tls:
            return tls.getpeercert(binary_form=True)


def replace(path, data):
    with tempfile.NamedTemporaryFile(dir=path.parent, delete=False) as staged:
        temp = Path(staged.name)
        try:
            staged.write(data)
            staged.close()
            shutil.copystat(path, temp)
            stat = path.stat()
            os.chown(temp, stat.st_uid, stat.st_gid)
            temp.replace(path)
        finally:
            temp.unlink(missing_ok=True)


def apply(changes, families):
    original = {path: path.read_bytes() for path in changes}
    certificates = {address: certificate(address) for address in families}
    version = run("/usr/sbin/nginx", "-V")
    if "--with-stream=dynamic" in version and not re.search(
        r"^\s*load_module\s+[^;]*ngx_stream_module\.so\s*;", run("/usr/sbin/nginx", "-T"), re.M
    ):
        if subprocess.call(
            ["apt-get", "install", "-y", "libnginx-mod-stream"],
            env={**os.environ, "DEBIAN_FRONTEND": "noninteractive"},
            stdout=sys.stderr,
        ):
            raise ValueError("could not install the Ubuntu Nginx stream module")

    BACKUP.mkdir(mode=0o700)
    changed = []
    reload_attempted = False
    try:
        for path, data in original.items():
            saved = BACKUP / path.relative_to(ROOT)
            saved.parent.mkdir(parents=True, exist_ok=True)
            saved.write_bytes(data)
        for path, source in changes.items():
            if path.read_bytes() != original[path]:
                raise ValueError("Nginx configuration changed during installation")
            replace(path, source.encode("utf-8", errors="surrogateescape"))
            changed.append(path)
        run("/usr/sbin/nginx", "-t")
        reload_attempted = True
        run("systemctl", "reload", "nginx.service")
        for _ in range(15):
            try:
                if run("ss", "-H", "-ltnp", f"sport = :{SITE_PORT}").count('("nginx",') and all(
                    certificate(address) == expected for address, expected in certificates.items()
                ):
                    return
            except OSError:
                pass
            time.sleep(1)
        raise ValueError("HTTPS did not pass verification after reload")
    except (OSError, ValueError, subprocess.CalledProcessError):
        try:
            for path in changed:
                replace(path, original[path])
            if reload_attempted:
                run("/usr/sbin/nginx", "-t")
                run("systemctl", "reload", "nginx.service")
            shutil.rmtree(BACKUP)
        except (OSError, subprocess.CalledProcessError):
            raise ValueError(f"automatic rollback failed; original site files are in {BACKUP}") from None
        raise ValueError("TCP 443 sharing failed; original Nginx configuration was restored") from None


try:
    with open("/run/lock/rah-nginx-reality.lock", "a") as lock:
        fcntl.flock(lock, fcntl.LOCK_EX)
        changes, families = plan()
        if changes and BACKUP.exists():
            raise ValueError(f"existing Nginx backup at {BACKUP} needs review before retry")
        if MODE == "apply":
            if changes:
                apply(changes, families)
            else:
                run("systemctl", "reload", "nginx.service")
        else:
            for address in families:
                certificate(address)
except ValueError as error:
    print(f"Nginx: {error}")
    sys.exit(1)
except (OSError, subprocess.CalledProcessError):
    print("Nginx: could not read the active configuration or verify existing HTTPS")
    sys.exit(1)
