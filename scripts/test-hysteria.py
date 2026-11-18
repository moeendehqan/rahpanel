#!/usr/bin/env python3
"""Run generated Rah configs with real Xray/Mihomo on isolated loopback ports."""
import copy
import http.server
import json
import os
import pathlib
import select as io_select
import socket
import struct
import subprocess
import sys
import threading
import time
import urllib.request
import urllib.error

directory = pathlib.Path(sys.argv[1])
xray, mihomo = sys.argv[2:]
fixture = json.loads((directory / "fixture.json").read_text())
processes = []
samples = []
run = 0
core_process = None
user = fixture["user"]
available = {"reality": True, "hysteria2": True}


def write(name, value):
    path = directory / name
    path.write_text(json.dumps(value))
    return str(path)


class Destination(http.server.BaseHTTPRequestHandler):
    def log_message(self, *args):
        pass

    def do_GET(self):
        if self.path == "/health":
            self.send_response(204)
            self.end_headers()
        elif self.path == "/stream":
            self.send_response(200)
            self.end_headers()
            try:
                for _ in range(400):
                    self.wfile.write(b"x" * 1024)
                    self.wfile.flush()
                    time.sleep(0.03)
            except (BrokenPipeError, ConnectionResetError):
                pass
        else:
            self.send_response(200)
            self.send_header("Content-Length", "16384")
            self.end_headers()
            self.wfile.write(b"x" * 16384)


destination = http.server.ThreadingHTTPServer(("127.0.0.1", 15081), Destination)
threading.Thread(target=destination.serve_forever, daemon=True).start()
echo = socket.socket(socket.AF_INET, socket.SOCK_DGRAM)
echo.bind(("127.0.0.1", 15082))


def serve_echo():
    while True:
        data, address = echo.recvfrom(65535)
        echo.sendto(data, address)


threading.Thread(target=serve_echo, daemon=True).start()


def relay_tcp(connection):
    try:
        with connection, socket.create_connection(("127.0.0.1", 14445), timeout=2) as upstream:
            while available["reality"]:
                ready, _, _ = io_select.select([connection, upstream], [], [], 0.2)
                for source in ready:
                    data = source.recv(65535)
                    if not data:
                        return
                    (upstream if source is connection else connection).sendall(data)
    except OSError:
        connection.close()


def serve_tcp_relay():
    with socket.socket() as listener:
        listener.setsockopt(socket.SOL_SOCKET, socket.SO_REUSEADDR, 1)
        listener.bind(("127.0.0.1", 14444))
        listener.listen()
        while True:
            connection, _ = listener.accept()
            if available["reality"]:
                threading.Thread(target=relay_tcp, args=(connection,), daemon=True).start()
            else:
                connection.close()


def serve_udp_relay():
    with socket.socket(socket.AF_INET, socket.SOCK_DGRAM) as listener:
        listener.bind(("127.0.0.1", 14443))
        peers = {}

        def responses(upstream, address):
            while True:
                try:
                    packet = upstream.recv(65535)
                except ConnectionRefusedError:
                    continue
                if available["hysteria2"]:
                    listener.sendto(packet, address)

        while True:
            packet, address = listener.recvfrom(65535)
            if not available["hysteria2"]:
                continue
            if address not in peers:
                upstream = socket.socket(socket.AF_INET, socket.SOCK_DGRAM)
                upstream.connect(("127.0.0.1", 14446))
                peers[address] = upstream
                threading.Thread(target=responses, args=(upstream, address), daemon=True).start()
            peers[address].send(packet)


threading.Thread(target=serve_tcp_relay, daemon=True).start()
threading.Thread(target=serve_udp_relay, daemon=True).start()


def launch(command, name):
    log = open(directory / (name + ".log"), "ab")
    env = dict(os.environ, SSL_CERT_FILE=str(directory / "ca.crt"))
    process = subprocess.Popen(command, stdout=log, stderr=log, env=env)
    log.close()
    processes.append(process)
    return process


def wait_port(port):
    for _ in range(100):
        try:
            with socket.create_connection(("127.0.0.1", port), timeout=0.2):
                return
        except OSError:
            time.sleep(0.05)
    raise AssertionError("listener did not start: " + str(port))


def stats():
    data = json.loads(subprocess.check_output([xray, "api", "statsquery", "--server=127.0.0.1:15085"]))
    values = {item["name"]: item.get("value", 0) for item in data.get("stat", [])}
    samples.append({"run": str(run), "values": values})
    return sum(value for name, value in values.items() if name.startswith("user>>>" + user + ">>>traffic>>>"))


def restart(kind="enabled"):
    global run, core_process
    if core_process:
        stats()
        core_process.terminate()
        core_process.wait(timeout=5)
    cfg = copy.deepcopy(fixture["configs"][kind])
    for inbound in cfg["inbounds"]:
        inbound["listen"] = "127.0.0.1"
        if inbound.get("tag") == "hysteria2":
            inbound["port"] = 14446
        elif inbound.get("tag") == "reality":
            inbound["port"] = 14445
        tls = inbound.get("streamSettings", {}).get("tlsSettings")
        if tls:
            tls["certificates"] = [{"certificateFile": str(directory / "tls.crt"), "keyFile": str(directory / "tls.key")}]
    # The fixture destinations are loopback-only. Keep the production private-IP
    # block in the generated source config and override it only inside this lab.
    cfg["routing"]["rules"] = cfg["routing"]["rules"][:1]
    cfg["outbounds"][0]["settings"]["finalRules"] = [{"action": "allow", "ip": ["127.0.0.1"]}]
    path = write("server.json", cfg)
    subprocess.run([xray, "run", "-test", "-config", path], check=True, stdout=subprocess.DEVNULL)
    run += 1
    core_process = launch([xray, "run", "-config", path], "xray")
    wait_port(15085)
    time.sleep(0.2)


def api(path, method="GET", payload=None):
    request = urllib.request.Request("http://127.0.0.1:15090" + path, method=method)
    if payload is not None:
        request.data = json.dumps(payload).encode()
        request.add_header("Content-Type", "application/json")
    with urllib.request.urlopen(request, timeout=8) as response:
        body = response.read()
        return json.loads(body) if body else None


def select(name):
    api("/proxies/Rah", "PUT", {"name": name})
    api("/connections", "DELETE")


def request(path="/", timeout=10, port=15080):
    return subprocess.run(["curl", "-fsS", "--noproxy", "", "--proxy", "http://127.0.0.1:" + str(port),
                           "--max-time", str(timeout), "http://127.0.0.1:15081" + path], capture_output=True)


def transfer():
    result = request()
    assert result.returncode == 0 and result.stdout == b"x" * 16384, result.stderr.decode()


def udp_transfer():
    with socket.create_connection(("127.0.0.1", 15080), timeout=4) as control:
        control.sendall(b"\x05\x01\x00")
        assert control.recv(2) == b"\x05\x00"
        control.sendall(b"\x05\x03\x00\x01" + b"\x00" * 6)
        reply = control.recv(256)
        assert reply[:4] == b"\x05\x00\x00\x01", reply
        address, port = socket.inet_ntoa(reply[4:8]), struct.unpack("!H", reply[8:10])[0]
        with socket.socket(socket.AF_INET, socket.SOCK_DGRAM) as client:
            client.settimeout(4)
            packet = b"\x00\x00\x00\x01" + socket.inet_aton("127.0.0.1") + struct.pack("!H", 15082) + b"udp-accounting" * 80
            for _ in range(4):
                client.sendto(packet, (address, port))
                response, _ = client.recvfrom(65535)
                assert response[10:] == packet[10:]
            return 4 * len(packet[10:])


def healthcheck():
    try:
        api("/group/Rah%20Auto/delay?url=http%3A%2F%2F127.0.0.1%3A15081%2Fhealth&timeout=1500")
    except urllib.error.HTTPError as error:
        if error.code != 504:
            raise


def wait_transfer(expected):
    for _ in range(15):
        healthcheck()
        result = request(timeout=2)
        if result.returncode == 0 and result.stdout == b"x" * 16384 and api("/proxies/Rah%20Auto")["now"] == expected:
            return
        time.sleep(1)
    raise AssertionError("automatic fallback did not select " + expected)


try:
    proxies, groups = [], []
    section = None
    for line in fixture["subscription"].splitlines():
        if line == "proxies:":
            section = proxies
        elif line == "proxy-groups:":
            section = groups
        elif line.startswith("  - "):
            section.append(json.loads(line[4:]))
    for proxy in proxies:
        proxy["server"] = "127.0.0.1"
        proxy["udp"] = True
        if "reality-opts" in proxy:
            settings = next(item for item in fixture["configs"]["enabled"]["inbounds"] if item["tag"] == "reality")["streamSettings"]["realitySettings"]
            derived = subprocess.check_output([xray, "x25519", "-i", settings["privateKey"]]).decode()
            assert proxy["reality-opts"]["public-key"] in derived, "REALITY key mismatch"
            assert proxy["reality-opts"]["short-id"] in settings["shortIds"], "REALITY short-id mismatch"
        if proxy["type"] == "hysteria2":
            proxy["port"] = 14443
    names = {("websocket" if proxy.get("network") == "ws" else proxy["type"]): proxy["name"] for proxy in proxies}
    for group in groups:
        if group["type"] == "fallback":
            group.update(url="http://127.0.0.1:15081/health", interval=1, timeout=1500, lazy=False)
    config = {"mixed-port": 15080, "external-controller": "127.0.0.1:15090", "mode": "rule",
              "tls": {"custom-certifactes": [(directory / "ca.crt").read_text()]},
              "log-level": "warning", "proxies": proxies, "proxy-groups": groups, "rules": ["MATCH,Rah"]}
    restart()
    reality_proxy = next(proxy for proxy in proxies if "reality-opts" in proxy)
    probe_config = {"inbounds": [{"listen": "127.0.0.1", "port": 15089, "protocol": "socks"}],
                    "outbounds": [{"protocol": "vless", "settings": {"vnext": [{"address": "127.0.0.1", "port": 14444,
                        "users": [{"id": reality_proxy["uuid"], "encryption": "none", "flow": reality_proxy["flow"]}]}]},
                        "streamSettings": {"network": "tcp", "security": "reality", "realitySettings": {
                            "serverName": reality_proxy["servername"], "fingerprint": "chrome",
                            "publicKey": reality_proxy["reality-opts"]["public-key"], "shortId": reality_proxy["reality-opts"]["short-id"]}}}]}
    launch([xray, "run", "-config", write("probe.json", probe_config)], "probe")
    wait_port(15089)
    check = subprocess.run(["curl", "-fsS", "--noproxy", "", "--socks5-hostname", "127.0.0.1:15089",
                            "--max-time", "12", "http://127.0.0.1:15081/"], capture_output=True)
    assert check.returncode == 0 and len(check.stdout) == 16384, "Xray REALITY probe failed: " + check.stderr.decode()
    print("PASS Xray REALITY probe", flush=True)
    launch([mihomo, "-d", str(directory), "-f", write("mihomo.json", config)], "mihomo")
    wait_port(15090)
    select(names["vless"])
    before = stats()
    transfer()
    after_reality = stats()
    assert after_reality > before, "REALITY traffic missing from user counters"
    print("PASS REALITY with generated config", flush=True)
    select(names["hysteria2"])
    transfer()
    after_hysteria = stats()
    assert after_hysteria > after_reality, "Hysteria TCP traffic missing from shared counters"
    udp_bytes = udp_transfer()
    time.sleep(0.3)
    assert stats() - after_hysteria >= 2 * udp_bytes, "Hysteria UDP upload/download missing from shared counters"
    print("PASS Hysteria TCP/UDP, certificate verification, shared user counters", flush=True)
    bad = copy.deepcopy(config)
    bad["mixed-port"], bad["external-controller"] = 15086, "127.0.0.1:15091"
    bad["proxies"] = [copy.deepcopy(next(proxy for proxy in proxies if proxy["type"] == "hysteria2"))]
    bad["proxies"][0]["sni"] = "wrong.test"
    bad["proxy-groups"] = [{"name": "Rah", "type": "select", "proxies": [names["hysteria2"]]}]
    bad_dir = directory / "invalid-certificate"
    bad_dir.mkdir()
    launch([mihomo, "-d", str(bad_dir), "-f", write("bad-certificate.json", bad)], "bad-certificate")
    wait_port(15091)
    assert request(timeout=3, port=15086).returncode != 0, "invalid TLS identity was accepted"
    print("PASS Hysteria rejects invalid TLS identity", flush=True)
    for protocol in ("websocket", "trojan"):
        select(names[protocol])
        transfer()
        print("PASS existing protocol regression: " + protocol, flush=True)
    select("Rah Auto")
    wait_transfer(names["vless"])
    available["reality"] = False
    wait_transfer(names["hysteria2"])
    available["reality"] = True
    available["hysteria2"] = False
    wait_transfer(names["vless"])
    available["hysteria2"] = True
    print("PASS automatic fallback with TCP and UDP paths interrupted independently", flush=True)
    for kind in ("suspended", "expired", "quota", "removed"):
        select(names["hysteria2"])
        transfer()
        active_path = directory / "active.data"
        with active_path.open("wb") as output:
            active = subprocess.Popen(["curl", "-fsSN", "--noproxy", "", "--proxy", "http://127.0.0.1:15080",
                                       "--max-time", "15", "http://127.0.0.1:15081/stream"], stdout=output, stderr=subprocess.DEVNULL)
            processes.append(active)
            time.sleep(0.5)
            assert active_path.stat().st_size > 0, "stream did not start"
            restart(kind)
            time.sleep(0.5)
            stopped_at = active_path.stat().st_size
            time.sleep(0.8)
            assert active_path.stat().st_size == stopped_at, "revoked stream continued delivering bytes"
            assert request(timeout=2).returncode != 0, "revoked user reconnected"
            active.terminate()
            active.wait(timeout=3)
        restart()
        select(names["hysteria2"])
        # QUIC reconnects following the server's closed connection.
        for attempt in range(20):
            restored = request(timeout=2)
            if restored.returncode == 0 and restored.stdout == b"x" * 16384:
                break
            time.sleep(0.3)
        else:
            raise AssertionError("access restoration failed")
        print("PASS active stream revocation, reconnect rejection and restoration: " + kind, flush=True)
    stats()
    write("results.json", {"samples": samples})
except Exception:
    for name in ("xray.log", "mihomo.log", "probe.log"):
        path = directory / name
        if path.exists():
            print(name + "\n" + path.read_text()[-10000:], flush=True)
    raise
finally:
    for process in reversed(processes):
        if process.poll() is None:
            process.terminate()
            try:
                process.wait(timeout=3)
            except subprocess.TimeoutExpired:
                process.kill()
                process.wait()
    destination.shutdown()
