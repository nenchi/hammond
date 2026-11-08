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
s, _ = call("POST", "/api/me/settings", {**base_settings, "showFillupTime": False}, token=tok)
check("settings: show fill-up time can be turned off (default on)", s == 200 and call("GET", "/api/me", token=tok)[1].get("showFillupTime") is False, s)
call("POST", "/api/me/settings", {**base_settings, "showFillupTime": True}, token=tok)
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

# ---- external API: API keys + detections (Home Assistant etc.) ----
s, v_upd = call("PUT", f"/api/vehicles/{vid}", {"nickname": "Smoke", "registration": "LJ SMOKE1", "make": "Skoda",
                                               "model": "Octavia", "fuelUnit": 0, "fuelType": 1, "tankCapacity": 50}, token=tok)
check("vehicle tank size saved", s == 200 and call("GET", f"/api/vehicles/{vid}", token=tok)[1].get("tankCapacity") == 50, (s, v_upd))
s, k = call("POST", "/api/me/apikeys", {"name": "Home Assistant"}, token=tok)
check("create API key (shown once)", s == 201 and k["key"].startswith("hmd_"), (s, k))
key = k["key"]
s, keys = call("GET", "/api/me/apikeys", token=tok)
check("API key list has no secret", s == 200 and len(keys) == 1 and key not in json.dumps(keys), (s, keys))
s, vs = call("GET", "/api/v1/vehicles", token=key)
check("v1: list vehicles with API key", s == 200 and vs[0]["id"] == vid and vs[0]["tankCapacity"] == 50, (s, vs))
s, _ = call("GET", "/api/v1/vehicles", token=tok)
check("v1: login token is not an API key (401)", s == 401, s)
s, _ = call("GET", "/api/v1/vehicles")
check("v1: no key is 401", s == 401, s)
s, _ = call("GET", "/api/me", token=key)
check("API key can't use the UI API (401)", s == 401, s)
s, _ = call("POST", "/api/v1/vehicles/00000000-0000-0000-0000-000000000000/detections", {"odoReading": 1, "fuelQuantity": 5}, token=key)
check("v1: unknown/foreign vehicle is 404", s == 404, s)
s, r = call("POST", f"/api/v1/vehicles/{vid}/detections", {"odoReading": 11200, "levelBefore": 20, "levelAfter": 98,
            "odoAtBefore": 11198, "source": "Home Assistant"}, token=key)
check("v1: detection from fuel levels (78% of 50 l = 39 l)", s == 201 and r["result"] == "pending"
      and r["detection"]["fuelQuantity"] == 39 and r["detection"]["isTankFull"] and "~39.0" in r["message"], (s, r))
did = r["detection"]["id"]
s, r2 = call("POST", f"/api/v1/vehicles/{vid}/detections", {"odoReading": 10650, "fuelQuantity": 42}, token=key)
check("v1: detection near an existing fill-up asks (duplicate)", s == 201 and r2["result"] == "duplicate", (s, r2))
s, r3 = call("POST", f"/api/v1/vehicles/{vid}/detections", {"odoReading": 11300, "levelBefore": 90, "levelAfter": 50}, token=key)
check("v1: falling level refused (422)", s == 422, (s, r3))
s, pend = call("GET", "/api/detections", token=tok)
check("pending detections listed", s == 200 and {d["id"] for d in pend} == {did, r2["detection"]["id"]}, (s, pend))
s, _ = call("POST", f"/api/detections/{r2['detection']['id']}/discard", token=tok)
s, f = call("POST", f"/api/vehicles/{vid}/fillups", {
    "vehicleId": vid, "fuelUnit": 0, "fuelQuantity": 38.6, "perUnitPrice": 1.6, "totalAmount": 61.76, "odoReading": 11200,
    "isTankFull": True, "userId": me["id"], "date": "2026-11-07T12:00:00Z", "detectionId": did}, token=tok)
s2, pend = call("GET", "/api/detections", token=tok)
check("completing a detection creates the fill-up and clears it", s == 201 and s2 == 200 and pend == [], (s, f, pend))
kid = keys[0]["id"]
s, _ = call("DELETE", f"/api/me/apikeys/{kid}", token=tok)
s2, _ = call("GET", "/api/v1/vehicles", token=key)
check("revoked API key stops working", s2 == 401, (s, s2))

# ---- export / backup / admin checks ----
s, csvtxt = call("GET", f"/api/vehicles/{vid}/export", token=tok, raw=True)
check("vehicle CSV export (Fuelly format)", s == 200 and csvtxt.startswith("Type,MPG,Date,Time,Car Name") and "\nGas," in csvtxt, (s, csvtxt[:200]))
s, _ = call("GET", "/api/vehicles/00000000-0000-0000-0000-000000000000/export", token=tok, raw=True)
check("export of a foreign/unknown vehicle is 404", s == 404, s)
req = urllib.request.Request(BASE + "/api/backup", headers={"Authorization": "Bearer " + tok})
with urllib.request.urlopen(req, timeout=30) as r:
    import gzip as _gz
    blob = r.read()
check("admin backup download is a gzip'ed SQLite db", _gz.decompress(blob)[:15] == b"SQLite format 3", len(blob))
s, _ = call("POST", "/api/register", {"name": "Plain", "email": "plain@example.com", "password": "plain-password1"}, token=tok)
check("admin can register a user", s in (200, 201), s)
s, plain = call("POST", "/api/login", {"email": "plain@example.com", "password": "plain-password1"})
ptok = plain["token"]
s, _ = call("GET", "/api/backup", token=ptok, raw=True)
check("non-admin can't download the backup (401)", s == 401, s)
s, _ = call("POST", "/api/register", {"name": "Sneaky", "email": "sneaky@example.com", "password": "sneaky-password1"}, token=ptok)
s2, _ = call("POST", "/api/login", {"email": "sneaky@example.com", "password": "sneaky-password1"})
check("non-admin can't create users (upstream could)", s == 401 and s2 != 200, (s, s2))
s, _ = call("POST", "/api/settings", {"currency": "USD", "distanceUnit": 0}, token=ptok)
check("non-admin can't change site settings", s == 401, s)

# ---- station prices (fake Tankerkönig on 127.0.0.1:3999 in CI) ----
s, r = call("GET", f"/api/vehicles/{vid}/stations?lat=50.1&lng=8.76", token=tok)
check("stations: clear error while no provider is set up", s == 422 and "not set up" in r, (s, r))
s, st = call("POST", "/api/settings/fuelPrices", {"provider": "custom", "url": "http://127.0.0.1:3999/json", "apiKey": "ci-key"}, token=tok)
check("admin sets the price provider; key not echoed", s == 200 and st.get("fuelPriceKeySet") is True and "ci-key" not in json.dumps(st), (s, st))
s, pub = call("GET", "/api/settings", token=ptok)
check("site settings readable by users without the key", s == 200 and "ci-key" not in json.dumps(pub) and pub.get("fuelPriceProvider") == "custom", pub)
s, _ = call("POST", "/api/settings/fuelPrices", {"provider": "tankerkoenig", "apiKey": "x"}, token=ptok)
check("non-admin can't change the price provider", s == 401, s)
call("PUT", f"/api/vehicles/{vid}", {"nickname": "Smoke", "registration": "LJ SMOKE1", "make": "Skoda", "model": "Octavia",
     "fuelUnit": 0, "fuelType": 1, "tankCapacity": 50, "fuelGrade": "e10"}, token=tok)
s, r = call("GET", f"/api/vehicles/{vid}/stations?lat=50.1&lng=8.76", token=tok)
check("stations nearby with E10 prices", s == 200 and r["grade"] == "e10" and r["stations"][0]["price"] == 1.739
      and r["stations"][1]["price"] is None and "MTS-K" in r["attribution"], (s, r))

s, ref = call("POST", "/api/refresh", {"refreshToken": login["refreshToken"]}, token=tok)
check("refresh token", s == 200 and ref.get("token"), (s, ref))
s, _ = call("POST", "/api/refresh", {"refreshToken": "garbage"}, token=tok)
check("garbage refresh token is 401 (no crash)", s == 401, s)

print("all smoke tests passed")
