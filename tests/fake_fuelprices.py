"""Fake Tankerkönig list.php for CI (python3 tests/fake_fuelprices.py [port]): fixed stations around
any position; key "ci-key" required, like the real API needs one. Also a fake Home Assistant
/api/states/sensor.ci_odometer (token "ci-ha-token"), a fake EVCC /api/sessions and a fake Gotify
POST /message (key "ci-gotify"; GET /messages lists what it got)."""
import json, sys
from datetime import datetime, timedelta, timezone
from http.server import BaseHTTPRequestHandler, HTTPServer
from urllib.parse import parse_qs, urlparse

STATIONS = [
    {"id": "aral", "name": "ARAL Tankstelle", "brand": "ARAL", "street": "Frankfurter Str.", "houseNumber": "12",
     "postCode": 63067, "place": "Offenbach", "lat": 50.1031, "lng": 8.7601, "dist": 0.4, "diesel": 1.629, "e5": 1.799, "e10": 1.739, "isOpen": True},
    {"id": "free", "name": "Freie Tankstelle", "brand": "", "street": "Hafen", "houseNumber": "", "postCode": 63067,
     "place": "Offenbach", "dist": 1.1, "diesel": 1.599, "e5": 1.769, "e10": False, "isOpen": True},
]

MESSAGES = []


class H(BaseHTTPRequestHandler):
    def reply(self, code, body):
        data = json.dumps(body).encode()
        self.send_response(code)
        self.send_header("Content-Type", "application/json")
        self.send_header("Content-Length", str(len(data)))
        self.end_headers()
        self.wfile.write(data)

    def do_POST(self):
        if urlparse(self.path).path != "/message":
            return self.reply(404, {})
        if self.headers.get("X-Gotify-Key") != "ci-gotify":
            return self.reply(401, {"error": "Unauthorized"})
        MESSAGES.append(json.loads(self.rfile.read(int(self.headers.get("Content-Length", 0)))))
        self.reply(200, {"id": len(MESSAGES)})

    def do_GET(self):
        u = urlparse(self.path)
        q = parse_qs(u.query)
        if u.path == "/messages":
            return self.reply(200, MESSAGES)
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
        body = ({"ok": False, "message": "apikey falsch"} if q.get("apikey") != ["ci-key"] else
                {"ok": True, "license": "CC BY 4.0", "data": "MTS-K (CI fake)", "status": "ok", "stations": STATIONS})
        data = json.dumps(body).encode()
        self.send_response(200)
        self.send_header("Content-Type", "application/json")
        self.send_header("Content-Length", str(len(data)))
        self.end_headers()
        self.wfile.write(data)

    def log_message(self, *a):
        pass


HTTPServer(("127.0.0.1", int(sys.argv[1]) if len(sys.argv) > 1 else 3999), H).serve_forever()
