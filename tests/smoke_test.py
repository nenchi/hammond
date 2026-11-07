"""End-to-end smoke test against a running hammond container: python3 tests/smoke_test.py http://127.0.0.1:3000

Covers the paths a dependency update can break: UI assets, setup, login, JWT auth + refresh, vehicles,
fill-ups, stats, and two fork security fixes (no admin creation after setup, no empty JWT key).
"""
import base64, hashlib, hmac, json, re, sys, time, urllib.error, urllib.request

BASE = sys.argv[1].rstrip("/") if len(sys.argv) > 1 else "http://127.0.0.1:3000"
EMAIL, PASSWORD = "admin@example.com", "smoke-test-pass"


def call(method, path, body=None, token=None, raw=False):
    req = urllib.request.Request(BASE + path, method=method,
                                 data=json.dumps(body).encode() if body is not None else None)
    req.add_header("Content-Type", "application/json")
    if token:
        req.add_header("Authorization", "Bearer " + token)
    try:
        with urllib.request.urlopen(req, timeout=10) as r:
            data = r.read()
            return r.status, data.decode() if raw else (json.loads(data) if data else None)
    except urllib.error.HTTPError as e:
        return e.code, e.read().decode(errors="replace")


def check(name, cond, detail=""):
    print(("ok   " if cond else "FAIL ") + name + ("" if cond else f"  -> {detail}"), flush=True)
    if not cond:
        sys.exit(1)


def jwt_hs256(claims, key):
    b64 = lambda b: base64.urlsafe_b64encode(b).rstrip(b"=")
    msg = b64(b'{"alg":"HS256","typ":"JWT"}') + b"." + b64(json.dumps(claims).encode())
    return (msg + b"." + b64(hmac.new(key, msg, hashlib.sha256).digest())).decode()


for _ in range(30):
    try:
        if call("GET", "/api/system/status")[0] == 200:
            break
    except OSError:
        pass
    time.sleep(1)

s, html = call("GET", "/", raw=True)
check("UI index.html", s == 200 and re.search(r"<div id=\"?app", html), (s, html[:200]))
js = re.findall(r'src="?(/js/app\.[^" >]+\.js)', html)
check("UI app bundle referenced", bool(js), html[:300])
s, body = call("GET", js[0], raw=True)
check("UI app bundle served", s == 200 and len(body) > 1000, s)
s, body = call("GET", "/vehicles", raw=True)
check("SPA fallback route serves index.html", s == 200 and "app" in body, s)

s, st = call("GET", "/api/system/status")
check("system not initialized", s == 200 and st.get("initialized") is False, (s, st))

s, r = call("POST", "/api/auth/initialize", {"name": "Admin", "email": EMAIL, "password": PASSWORD,
                                             "currency": "EUR", "distanceUnit": 1})
check("initialize creates admin", s == 201 or s == 200, (s, r))

s, r = call("POST", "/api/auth/initialize", {"name": "Evil", "email": "evil@example.com",
                                             "password": "evil-password", "currency": "EUR", "distanceUnit": 1})
check("second initialize refused", s == 422, (s, r))
s, r = call("POST", "/api/login", {"email": "evil@example.com", "password": "evil-password"})
check("no admin created by second initialize", s != 200, (s, r))

s, login = call("POST", "/api/login", {"email": EMAIL, "password": PASSWORD})
check("login", s == 200 and login.get("token") and login.get("refreshToken"), (s, login))
tok = login["token"]

s, me = call("GET", "/api/me", token=tok)
check("GET /api/me with token", s == 200 and me.get("email") == EMAIL, (s, me))
s, _ = call("GET", "/api/me")
check("GET /api/me without token is 401", s == 401, s)
forged = jwt_hs256({"id": me["id"], "role": 0, "exp": int(time.time()) + 3600}, b"")
s, _ = call("GET", "/api/me", token=forged)
check("token signed with empty key rejected", s == 401, s)
none_alg = (base64.urlsafe_b64encode(b'{"alg":"none","typ":"JWT"}').rstrip(b"=") + b"."
            + base64.urlsafe_b64encode(json.dumps({"id": me["id"]}).encode()).rstrip(b"=") + b".").decode()
s, _ = call("GET", "/api/me", token=none_alg)
check("alg=none token rejected", s == 401, s)

s, m = call("GET", "/api/masters", token=tok)
check("masters", s == 200 and m, (s, str(m)[:200]))

s, v = call("POST", "/api/vehicles", {"nickname": "Smoke", "registration": "LJ SMOKE1", "make": "Skoda",
                                      "model": "Octavia", "yearOfManufacture": 2020, "engineSize": 1.6,
                                      "fuelUnit": 0, "fuelType": 1}, token=tok)
check("create vehicle", s == 201 and v.get("id"), (s, v))
vid = v["id"]

for odo, qty in ((10000, 40.0), (10650, 42.5)):
    s, f = call("POST", f"/api/vehicles/{vid}/fillups", {
        "vehicleId": vid, "fuelUnit": 0, "fuelQuantity": qty, "perUnitPrice": 1.55,
        "totalAmount": round(qty * 1.55, 2), "odoReading": odo, "isTankFull": True, "HasMissedFillup": False,
        "fillingStation": "Petrol", "userId": me["id"], "date": "2026-11-07T10:00:00Z"}, token=tok)
    check(f"create fill-up odo {odo}", s == 201, (s, f))

s, fl = call("GET", f"/api/vehicles/{vid}/fillups", token=tok)
check("list fill-ups", s == 200 and len(fl) == 2, (s, fl))
s, stats = call("GET", f"/api/vehicles/{vid}/stats", token=tok)
check("vehicle stats", s == 200, (s, stats))
s, mv = call("GET", "/api/me/vehicles", token=tok)
check("my vehicles", s == 200 and any(x["id"] == vid for x in mv), (s, mv))

base_settings = {"currency": "EUR", "distanceUnit": 1, "dateFormat": "dd/MM/yyyy"}
s, _ = call("POST", "/api/me/settings", {**base_settings, "theme": "dark", "prefillFillup": False}, token=tok)
s2, me2 = call("GET", "/api/me", token=tok)
check("settings: theme + prefill saved", s == 200 and me2.get("theme") == "dark" and me2.get("prefillFillup") is False, (s, me2))
s, _ = call("POST", "/api/me/settings", base_settings, token=tok)
s2, me2 = call("GET", "/api/me", token=tok)
check("settings: old clients (no theme field) keep the stored theme", s == 200 and me2.get("theme") == "dark", (s, me2))
s, _ = call("POST", "/api/me/settings", {**base_settings, "theme": "pink"}, token=tok)
check("settings: invalid theme refused", s == 422, s)
s, _ = call("POST", "/api/me/settings", {"currency": "EUR"}, token=tok)
check("settings: missing distanceUnit is 422 (upstream crashed)", s == 422, s)
req = urllib.request.Request(BASE + "/api/me/settings", method="POST", data=b"{not json", headers={
    "Content-Type": "application/json", "Authorization": "Bearer " + tok})
try:
    urllib.request.urlopen(req, timeout=10); s = 200
except urllib.error.HTTPError as e:
    s = e.code
check("malformed JSON is 422, not a crash", s == 422, s)
s, _ = call("POST", "/api/me/settings", {**base_settings, "theme": "auto", "prefillFillup": True}, token=tok)

s, ref = call("POST", "/api/refresh", {"refreshToken": login["refreshToken"]}, token=tok)
check("refresh token", s == 200 and ref.get("token"), (s, ref))
s, _ = call("POST", "/api/refresh", {"refreshToken": "garbage"}, token=tok)
check("garbage refresh token is 401 (no crash)", s == 401, s)

print("all smoke tests passed")
