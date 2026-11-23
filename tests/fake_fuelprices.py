"""Fake Tankerkönig list.php for CI (python3 tests/fake_fuelprices.py [port]): fixed stations around
any position; key "ci-key" required, like the real API needs one. Also a fake Home Assistant
/api/states/sensor.ci_odometer (token "ci-ha-token"), a fake EVCC /api/sessions and a fake Gotify
POST /message (key "ci-gotify"; GET /messages lists what it got), and a fake MQTT broker on port + 1
(user "ci-mqtt"; GET /mqtt lists the last payload per topic)."""
import json, sys
from datetime import datetime, timedelta, timezone
from http.server import BaseHTTPRequestHandler, HTTPServer
from urllib.parse import parse_qs, unquote, urlparse

STATIONS = [
    {"id": "aral", "name": "ARAL Tankstelle", "brand": "ARAL", "street": "Frankfurter Str.", "houseNumber": "12",
     "postCode": 63067, "place": "Offenbach", "lat": 50.1031, "lng": 8.7601, "dist": 0.4, "diesel": 1.629, "e5": 1.799, "e10": 1.739, "isOpen": True},
    {"id": "free", "name": "Freie Tankstelle", "brand": "", "street": "Hafen", "houseNumber": "", "postCode": 63067,
     "place": "Offenbach", "dist": 1.1, "diesel": 1.599, "e5": 1.769, "e10": False, "isOpen": True},
]

MESSAGES = []
MQTT = {}  # topic -> last payload (retained like a broker), GET /mqtt lists them


def mqtt_broker(port):
    """Minimal MQTT 3.1.1 broker for CI: CONNECT/CONNACK, PUBLISH (QoS 0/1 + PUBACK), PINGREQ; keeps the last
    payload per topic. Enough for hammond's publisher, nothing else."""
    import socket, threading

    def read(c, n):
        b = b""
        while len(b) < n:
            chunk = c.recv(n - len(b))
            if not chunk:
                raise EOFError
            b += chunk
        return b

    def client(c):
        try:
            while True:
                head = read(c, 1)[0]
                length, mult = 0, 1
                while True:
                    d = read(c, 1)[0]
                    length += (d & 127) * mult
                    mult *= 128
                    if d < 128:
                        break
                body = read(c, length) if length else b""
                kind = head >> 4
                if kind == 1:  # CONNECT
                    user = b""
                    # protocol name ("MQTT", or "MQIsdp" when a client retries with 3.1), level, flags, keepalive
                    nl = int.from_bytes(body[:2], "big")
                    flags, p = body[2 + nl + 1], 2 + nl + 4
                    if flags & 0x80:  # username: after client id (+ will topic/message)
                        i = p + 2 + int.from_bytes(body[p:p + 2], "big")
                        if flags & 0x04:
                            for _ in range(2):
                                i += 2 + int.from_bytes(body[i:i + 2], "big")
                        user = body[i + 2:i + 2 + int.from_bytes(body[i:i + 2], "big")]
                    c.sendall(bytes([0x20, 2, 0, 0 if user in (b"", b"ci-mqtt") else 5]))
                elif kind == 3:  # PUBLISH
                    tl = int.from_bytes(body[:2], "big")
                    topic, rest = body[2:2 + tl].decode(), body[2 + tl:]
                    if (head >> 1) & 3:
                        pid, rest = rest[:2], rest[2:]
                        c.sendall(bytes([0x40, 2]) + pid)
                    MQTT[topic] = rest.decode()
                elif kind == 12:  # PINGREQ
                    c.sendall(bytes([0xD0, 0]))
                elif kind == 14:  # DISCONNECT
                    break
        except (EOFError, OSError):
            pass
        c.close()

    srv = socket.socket()
    srv.setsockopt(socket.SOL_SOCKET, socket.SO_REUSEADDR, 1)
    srv.bind(("127.0.0.1", port))
    srv.listen()
    while True:
        conn, _ = srv.accept()
        threading.Thread(target=client, args=(conn,), daemon=True).start()


class H(BaseHTTPRequestHandler):
    def reply(self, code, body):
        data = json.dumps(body).encode()
        self.send_response(code)
        self.send_header("Content-Type", "application/json")
        self.send_header("Content-Length", str(len(data)))
        self.end_headers()
        self.wfile.write(data)

    def do_POST(self):
        if urlparse(self.path).path == "/ntfy/":  # ntfy JSON publish (server under /ntfy, token "ci-ntfy")
            if self.headers.get("Authorization") != "Bearer ci-ntfy":
                return self.reply(403, {})
            m = json.loads(self.rfile.read(int(self.headers.get("Content-Length", 0))))
            MESSAGES.append({**m, "via": "ntfy"})
            return self.reply(200, {"id": "x"})
        if urlparse(self.path).path == "/mqtt/set":  # plant a retained message, e.g. a late last will "offline"
            m = json.loads(self.rfile.read(int(self.headers.get("Content-Length", 0))))
            MQTT[m["topic"]] = m["payload"]
            return self.reply(200, {})
        if urlparse(self.path).path != "/message":
            return self.reply(404, {})
        if self.headers.get("X-Gotify-Key") != "ci-gotify":
            return self.reply(401, {"error": "Unauthorized"})
        MESSAGES.append(json.loads(self.rfile.read(int(self.headers.get("Content-Length", 0)))))
        self.reply(200, {"id": len(MESSAGES)})

    def do_PUT(self):  # Matrix: send a message (access token "ci-matrix")
        p = urlparse(self.path).path
        if not p.startswith("/_matrix/client/v3/rooms/") or self.headers.get("Authorization") != "Bearer ci-matrix":
            return self.reply(401, {"errcode": "M_UNKNOWN_TOKEN"})
        m = json.loads(self.rfile.read(int(self.headers.get("Content-Length", 0))))
        MESSAGES.append({**m, "via": "matrix", "room": unquote(p.split("/")[5])})
        self.reply(200, {"event_id": "$x"})

    def do_GET(self):
        u = urlparse(self.path)
        q = parse_qs(u.query)
        if u.path.startswith("/_matrix/client/v3/directory/room/"):  # Matrix: room alias → ID
            alias = unquote(u.path.rsplit("/", 1)[1])
            return self.reply(200, {"room_id": "!" + alias[1:]}) if alias == "#fuel:ci" else self.reply(404, {})
        if u.path == "/messages":
            return self.reply(200, MESSAGES)
        if u.path == "/mqtt":  # what the fake MQTT broker (port + 1) received
            return self.reply(200, MQTT)
        if u.path == "/api/sessions":  # EVCC: one finished session of "CI EV" (no odometer)
            data = json.dumps([{"id": 7, "created": "2026-11-10T23:00:00Z", "finished": "2026-11-11T01:00:00Z",
                                "vehicle": "CI EV", "odometer": 0, "chargedEnergy": 12, "socEnd": 80, "pricePerKWh": 0.3}]).encode()
            self.send_response(200)
            self.send_header("Content-Type", "application/json")
            self.send_header("Content-Length", str(len(data)))
            self.end_headers()
            self.wfile.write(data)
            return
        if u.path == "/api/config":  # HA "Test connection"
            ok = self.headers.get("Authorization") == "Bearer ci-ha-token"
            data = json.dumps({"location_name": "CI Home", "version": "2026.11.1"}).encode()
            self.send_response(200 if ok else 401)
            self.send_header("Content-Type", "application/json")
            self.send_header("Content-Length", str(len(data)))
            self.end_headers()
            self.wfile.write(data)
            return
        if u.path.startswith("/api/history/period/"):  # HA history: two drives 4 h ago with a 5 min stop
            if self.headers.get("Authorization") != "Bearer ci-ha-token":
                return self.reply(401, {})
            now = datetime.now(timezone.utc)
            at = lambda m: (now - timedelta(minutes=m)).isoformat()
            series = {"sensor.ci_ignition": [(300, "0"), (240, "4"), (210, "0"), (205, "4"), (180, "0")],
                      "sensor.ci_odometer": [(300, "1000"), (209, "1040"), (179, "1075")],
                      "sensor.ci_fuel": [(300, "60"), (209, "50"), (179, "42")]}
            ids = q.get("filter_entity_id", [""])[0].split(",")
            if ids == ["device_tracker.ci_phone"]:  # the phone along both drives (home → work, with attributes)
                pos = [(242, 50.079, 8.77), (225, 50.09, 8.75), (211, 50.10, 8.73), (206, 50.10, 8.73), (190, 50.105, 8.70), (181, 50.11, 8.68)]
                return self.reply(200, [[{"entity_id": ids[0], "state": "not_home", "last_updated": at(m), "last_changed": at(m),
                                          "attributes": {"latitude": la, "longitude": ln, "gps_accuracy": 15}} for m, la, ln in pos]])
            return self.reply(200, [[{"entity_id": e, "state": st, "last_changed": at(m)} if i == 0 else
                                     {"state": st, "last_changed": at(m)} for i, (m, st) in enumerate(series[e])]
                                    for e in ids if e in series])
        if u.path.startswith("/api/states/"):
            ok = self.headers.get("Authorization") == "Bearer ci-ha-token"
            known = u.path == "/api/states/sensor.ci_odometer"
            body = {"entity_id": "sensor.ci_odometer", "state": "23456", "attributes": {"unit_of_measurement": "km"},
                    "last_updated": "2026-11-08T08:00:00+00:00", "last_reported": "2026-11-08T08:00:00+00:00"}
            data = json.dumps(body).encode()
            self.send_response(200 if ok and known else 401 if not ok else 404)
            self.send_header("Content-Type", "application/json")
            self.send_header("Content-Length", str(len(data)))
            self.end_headers()
            self.wfile.write(data)
            return
        if u.path != "/json/list.php":
            self.send_response(404); self.end_headers(); return
        # asked from ARAL's own position (a detection at the pump): it's 0 km away
        at_aral = q.get("lat") == ["50.1031"] and q.get("lng") == ["8.7601"]
        stations = [{**STATIONS[0], "dist": 0.0}, *STATIONS[1:]] if at_aral else STATIONS
        body = ({"ok": False, "message": "apikey falsch"} if q.get("apikey") != ["ci-key"] else
                {"ok": True, "license": "CC BY 4.0", "data": "MTS-K (CI fake)", "status": "ok", "stations": stations})
        data = json.dumps(body).encode()
        self.send_response(200)
        self.send_header("Content-Type", "application/json")
        self.send_header("Content-Length", str(len(data)))
        self.end_headers()
        self.wfile.write(data)

    def log_message(self, *a):
        pass


PORT = int(sys.argv[1]) if len(sys.argv) > 1 else 3999
import threading
threading.Thread(target=mqtt_broker, args=(PORT + 1,), daemon=True).start()
HTTPServer(("127.0.0.1", PORT), H).serve_forever()
