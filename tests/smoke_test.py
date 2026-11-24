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


def upload_photo(path, data, filename, token, origin="https://fuel.example.com"):
    """multipart PUT of a car photo (the UI's camera/file upload)"""
    bnd = "----hammondsmoke"
    body = (f"--{bnd}\r\nContent-Disposition: form-data; name=\"origin\"\r\n\r\n{origin}\r\n"
            f"--{bnd}\r\nContent-Disposition: form-data; name=\"file\"; filename=\"{filename}\"\r\n"
            f"Content-Type: image/png\r\n\r\n").encode() + data + f"\r\n--{bnd}--\r\n".encode()
    req = urllib.request.Request(BASE + path, method="PUT", data=body)
    req.add_header("Content-Type", "multipart/form-data; boundary=" + bnd)
    req.add_header("Authorization", "Bearer " + token)
    try:
        with urllib.request.urlopen(req, timeout=10) as r:
            return r.status, json.loads(r.read())
    except urllib.error.HTTPError as e:
        return e.code, e.read().decode(errors="replace")


png = base64.b64decode("iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR42mNk+M9QDwADhgGAWjR9awAAAABJRU5ErkJggg==")
s, r = upload_photo(f"/api/vehicles/{vid}/photo", png, "car.png", tok)
photo = r.get("photoUrl", "") if isinstance(r, dict) else ""
check("car photo uploaded: a random public link", s == 200 and photo.startswith("/api/public/photo/") and len(photo) == len("/api/public/photo/") + 32, (s, r))
with urllib.request.urlopen(BASE + photo, timeout=10) as pr:  # no login: <img> tags and Home Assistant
    check("car photo served without login", pr.status == 200 and pr.read() == png and pr.headers.get("Content-Type") == "image/png", pr.status)
check("vehicle shows its photo link", call("GET", f"/api/vehicles/{vid}", token=tok)[1].get("photoUrl") == photo)
s, _ = call("PUT", f"/api/vehicles/{vid}/photoHidden", {"hidden": True}, token=tok)
vv = call("GET", f"/api/vehicles/{vid}", token=tok)[1]
check("photo hidden in hammond keeps its link (for Home Assistant)", s == 200 and vv.get("photoHidden") is True and vv.get("photoUrl") == photo, (s, vv.get("photoHidden")))
s, r = upload_photo(f"/api/vehicles/{vid}/photo", b"<svg onload=alert(1)>", "car.png", tok)
check("a non-image is refused (bytes, not the name)", s == 422, (s, r))
s, _ = call("GET", "/api/public/photo/" + "0" * 32)
check("unknown photo link is 404", s == 404, s)
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
s, r2 = call("POST", f"/api/v1/vehicles/{vid}/detections", {"odoReading": 10650, "fuelQuantity": 42,
            "date": "2026-11-07T11:00:00Z"}, token=key)  # fixed date: the match window is ±48 h
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
# read-only v1 for the chat bot (homebot)
s, mv = call("GET", "/api/v1/me", token=key)
check("v1: me (currency, units, no email)", s == 200 and "currency" in mv and "distanceUnitShort" in mv and "email" not in mv, (s, mv))
s, st = call("GET", f"/api/v1/vehicles/{vid}/stats", token=key)
check("v1: vehicle stats", s == 200 and isinstance(st, list) and st and st[0]["countFillups"] >= 1, (s, st))
s, fl = call("GET", f"/api/v1/vehicles/{vid}/fillups", token=key)
check("v1: fill-ups", s == 200 and any(x["odoReading"] == 11200 for x in fl), (s, fl))
s, ml = call("GET", f"/api/v1/vehicles/{vid}/mileage?since=2000-01-01T00:00:00Z", token=key)
check("v1: mileage", s == 200 and isinstance(ml, list), (s, ml))
s, all_ml = call("GET", f"/api/vehicles/{vid}/mileage?since=2000-01-01T00:00:00Z", token=tok)
s2, none_ml = call("GET", f"/api/vehicles/{vid}/mileage?since=2000-01-01T00:00:00Z&until=2000-12-31T23:59:59Z", token=tok)
check("mileage: a custom range ends at its 'until'", s == 200 and s2 == 200 and len(all_ml) > 0 and none_ml == [], (len(all_ml), none_ml))
s, ex = call("GET", f"/api/v1/vehicles/{vid}/expenses", token=key)
check("v1: expenses", s == 200 and isinstance(ex, list), (s, ex))
for path in ("stats", "fillups", "expenses", "mileage"):
    s, _ = call("GET", f"/api/v1/vehicles/00000000-0000-0000-0000-000000000000/{path}", token=key)
    check(f"v1: {path} of a foreign vehicle is 404", s == 404, s)
s, pw = call("GET", "/api/v1/me/priceWatches", token=key)
check("v1: price watches", s == 200 and isinstance(pw, list), (s, pw))
s, _ = call("GET", "/api/v1/me/priceWatches/00000000-0000-0000-0000-000000000000/history", token=key)
check("v1: history of a foreign watch is 404", s == 404, s)
s, sm = call("GET", "/api/v1/summary", token=key)
check("v1: summary per vehicle and watch", s == 200 and [v["id"] for v in sm["vehicles"]] == [vid] and "monthFuel" in sm["vehicles"][0]
      and "tripsToday" in sm["vehicles"][0] and isinstance(sm["watches"], list), (s, sm))
# chat commands (Telegram / homebot) through /api/v1/command, writing through the UI API in-process
cmd = lambda text: call("POST", "/api/v1/command", {"text": text}, token=key)[1]["text"]
check("chat: cars", cmd("cars").startswith("**🚗 Cars\n1 · Smoke · LJ SMOKE1"), cmd("cars"))
check("chat: fill without km and no Home Assistant asks for them", cmd("fill 40.5 1.759").startswith("I need the odometer"), cmd("fill 40.5 1.759"))
r = cmd("/fill 40,5 1.759 11500 partial")
s, fl = call("GET", f"/api/vehicles/{vid}/fillups", token=tok)
new_f = [x for x in fl if x["odoReading"] == 11500]
check("chat: fill saves a fill-up (one car: no need to name it)", r.startswith("**✓ Fill-up saved · Smoke\n40.50 l × 1.759 = 71.24")
      and len(new_f) == 1 and new_f[0]["isTankFull"] is False and new_f[0]["comments"] == "from the chat", (r, new_f))
r = cmd("undo")
s, fl = call("GET", f"/api/vehicles/{vid}/fillups", token=tok)
check("chat: undo deletes it", r.startswith("↩️ deleted: fill-up 40.50 l") and not [x for x in fl if x["odoReading"] == 11500], (r, len(fl)))
check("chat: nothing left to undo", cmd("undo").startswith("nothing to undo"), cmd("undo"))
r = cmd("cost parking 3.50 Hbf Frankfurt")
s, ex = call("GET", f"/api/vehicles/{vid}/expenses", token=tok)
check("chat: cost without a trip today = an expense (comment keeps its case)", r.startswith("**✓ Parking 3.50 saved")
      and any(e["expenseType"] == "Parking" and e["amount"] == 3.5 and e["comments"] == "Hbf Frankfurt" for e in ex), (r, ex))
cmd("undo")
check("chat: purpose without trips", cmd("purpose Kunde Müller") == "no trip in the last 7 days", cmd("purpose x"))
s, v2c = call("POST", "/api/vehicles", {"nickname": "Panda", "registration": "LJ-CHAT 2", "make": "Fiat", "model": "Panda",
                                       "yearOfManufacture": 2020, "engineSize": 1200, "fuelUnit": 0, "fuelType": 0}, token=tok)
v2id = v2c["id"]
r = cmd("fill 10 1.5 11600")
check("chat: two cars and no default: fill asks which", r.startswith("Which car?\n1 · Panda · LJ-CHAT 2\n2 · Smoke"), r)
r = cmd("fill 10 1.5 900 ljchat2")
s, fl2 = call("GET", f"/api/vehicles/{v2id}/fillups", token=tok)
check("chat: a car by its plate (spaces and dashes ignored)", r.startswith("**✓ Fill-up saved · Panda") and len(fl2) == 1, (r, fl2))
cmd("undo")
check("chat: car 2 = default", cmd("car 2").startswith("⭐ default car: 2 · Smoke"), cmd("car 2"))
r = cmd("fill 10 1.5 11600")
check("chat: fill uses the default car", r.startswith("**✓ Fill-up saved · Smoke"), r)
cmd("undo")
check("chat: stats of one car by number", cmd("stats 1").startswith("**🚗 Panda") and "Smoke" not in cmd("stats 1"), cmd("stats 1"))
check("chat: stats of all cars", "**🚗 Panda" in cmd("stats") and "**🚗 Smoke" in cmd("stats"), cmd("stats"))
check("chat: unknown car lists them", cmd("stats golf").startswith("no vehicle matches golf\n1 · Panda"), cmd("stats golf"))
cmd("car none")
call("DELETE", f"/api/vehicles/{v2id}", token=tok)
s, d = call("POST", f"/api/v1/vehicles/{vid}/detections", {"odoReading": 11700, "fuelQuantity": 30.5, "source": "Home Assistant"}, token=key)
r = cmd("1,699")
s2, pend = call("GET", "/api/detections", token=tok)
s3, fl = call("GET", f"/api/vehicles/{vid}/fillups", token=tok)
done_f = [x for x in fl if x["odoReading"] == 11700]
check("chat: a bare price completes the detected fill-up", r.startswith("**✓ Detected fill-up saved · Smoke\n30.50 l × 1.699 = 51.82")
      and pend == [] and len(done_f) == 1 and done_f[0]["perUnitPrice"] == 1.699, (r, pend, done_f))
cmd("undo")
check("chat: a bare number without a detection is just unknown", cmd("1.5").startswith("unknown command"), cmd("1.5"))
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

# ---- UI API ID routes: only your own data (upstream hammond checked none) ----
s, fl = call("GET", f"/api/vehicles/{vid}/fillups", token=tok)
fid = fl[0]["id"]
for path in ("", "/stats", "/fillups", "/expenses", f"/fillups/{fid}", "/attachments", "/users"):
    s, _ = call("GET", f"/api/vehicles/{vid}{path}", token=ptok)
    check(f"other user: GET vehicle{path or ' itself'} is 404", s == 404, s)
s, _ = call("DELETE", f"/api/vehicles/{vid}/fillups/{fid}", token=ptok)
check("other user can't delete the fill-up (404, still there)", s == 404
      and call("GET", f"/api/vehicles/{vid}/fillups/{fid}", token=tok)[0] == 200, s)
s, _ = upload_photo(f"/api/vehicles/{vid}/photo", png, "car.png", ptok)
s2, _ = call("DELETE", f"/api/vehicles/{vid}/photo", token=ptok)
check("other user can't change or delete my car photo (404)", s == 404 and s2 == 404
      and call("GET", f"/api/vehicles/{vid}", token=tok)[1].get("photoUrl") == photo, (s, s2))
s, v2 = call("POST", "/api/vehicles", {"nickname": "Second", "registration": "LJ SMOKE2", "make": "Fiat", "model": "Panda",
                                      "fuelUnit": 0, "fuelType": 1}, token=tok)
v2 = v2["id"]
s, _ = call("GET", f"/api/vehicles/{v2}/fillups/{fid}", token=tok)
check("fill-up of another vehicle via your own vehicle's URL is 404", s == 404, s)
s, f2 = call("POST", f"/api/vehicles/{v2}/fillups", {"vehicleId": vid, "fuelUnit": 0, "fuelQuantity": 10, "perUnitPrice": 1.5,
             "totalAmount": 15, "odoReading": 100, "isTankFull": True, "userId": me["id"], "date": "2026-11-01T12:00:00Z"}, token=tok)
check("a new fill-up goes to the vehicle of the URL, not the body's vehicleId", s == 201 and f2["vehicleId"] == v2, (s, f2))
call("DELETE", f"/api/vehicles/{v2}", token=tok)
s, _ = call("GET", "/api/quickEntries", token=ptok)
check("non-admin can't list everyone's quick entries", s == 401, s)

# ---- station prices (fake Tankerkönig on 127.0.0.1:3999 in CI) ----
s, r = call("GET", f"/api/vehicles/{vid}/stations?lat=50.1&lng=8.76", token=tok)
# no profiles in Germany: the free sources (AT/SI/HR/FR/PT) are built in, Germany needs its key. With
# Nominatim reachable it says "add Germany – Tankerkönig"; without, the built-ins answer "no stations".
check("stations in Germany without a profile: says Tankerkönig is needed (or nothing nearby)",
      (s == 422 and "Tankerkönig" in r) or (s == 200 and r["stations"] == []), (s, r))
s, st = call("POST", "/api/settings/fuelPrices", {"profiles": [
    {"name": "Austria", "kind": "econtrol", "url": "http://127.0.0.1:3999/nothing-here"},  # fails: next profile answers
    {"name": "CI fake", "kind": "custom", "url": "http://127.0.0.1:3999/json", "apiKey": "ci-key"}]}, token=tok)
profiles = st.get("fuelPriceProfiles") or [] if s == 200 else []
check("admin sets price profiles; key not echoed", s == 200 and len(profiles) == 2 and profiles[1]["keySet"] is True
      and all(p["id"] for p in profiles) and "ci-key" not in json.dumps(st), (s, st))
s, pub = call("GET", "/api/settings", token=ptok)
check("site settings readable by users without the key", s == 200 and "ci-key" not in json.dumps(pub) and [p["kind"] for p in pub.get("fuelPriceProfiles", [])] == ["econtrol", "custom"], pub)
s, _ = call("POST", "/api/settings/fuelPrices", {"profiles": [{"name": "x", "kind": "tankerkoenig", "apiKey": "x"}]}, token=ptok)
check("non-admin can't change the site price profiles", s == 401, s)
# the admin makes the Austria profile private: users don't see it, admins do
s, st = call("POST", "/api/settings/fuelPrices", {"profiles": [{**profiles[0], "private": True}, profiles[1]]}, token=tok)
check("admin keeps a profile private", s == 200 and st["fuelPriceProfiles"][0]["private"] is True
      and st["fuelPriceProfiles"][1]["keySet"] is True, (s, st))
s, pub = call("GET", "/api/settings", token=ptok)
check("users don't see private site profiles", [p["name"] for p in pub["fuelPriceProfiles"]] == ["CI fake"], pub)
s, own = call("POST", "/api/me/fuelPrices", {"profiles": [{"name": "Mine", "kind": "custom", "url": "http://127.0.0.1:3999/json", "apiKey": "ci-key"}]}, token=ptok)
check("users add their own profile; key not echoed", s == 200 and len(own) == 1 and own[0]["keySet"] and "ci-key" not in json.dumps(own), (s, own))
s, av = call("GET", "/api/fuelPrices/available", token=ptok)
check("available for a user: own first, then shared, then the built-in free sources",
      s == 200 and [(p["name"], p["own"]) for p in av if not p["builtin"]] == [("Mine", True), ("CI fake", False)]
      and [p["kind"] for p in av if p["builtin"]] == ["econtrol", "goriva", "mzoe", "france", "portugal", "italy", "spain"], (s, av))
s, av = call("GET", "/api/fuelPrices/available", token=tok)
check("available for the admin: all site profiles (+ built-ins of other kinds)", s == 200
      and [p["name"] for p in av if not p["builtin"]] == ["Austria", "CI fake"] and "econtrol" not in [p["kind"] for p in av if p["builtin"]], (s, av))
s, r = call("GET", f"/api/settings/fuelPrices/test?profile={own[0]['id']}", token=ptok)
check("users test their own profile", s == 200 and r["ok"] and r["stations"] == 2, (s, r))
call("POST", "/api/me/fuelPrices", {"profiles": []}, token=ptok)
call("POST", "/api/settings/fuelPrices", {"profiles": profiles}, token=tok)
call("PUT", f"/api/vehicles/{vid}", {"nickname": "Smoke", "registration": "LJ SMOKE1", "make": "Skoda", "model": "Octavia",
     "fuelUnit": 0, "fuelType": 1, "tankCapacity": 50, "fuelGrade": "e10"}, token=tok)
s, r = call("GET", f"/api/vehicles/{vid}/stations?lat=50.1&lng=8.76", token=tok)
check("stations nearby with E10 prices", s == 200 and r["grades"] == ["e10"] and r["stations"][0]["price"] == 1.739
      and r["stations"][1]["price"] is None and "MTS-K" in r["attribution"] and r["profileName"] == "CI fake", (s, r))
s, g = call("GET", f"/api/vehicles/{vid}/gauge", token=tok)
check("gauge calibration: 4 segments, linear until 2 confirmed detections", s == 200 and g["segments"] == 4
      and len(g["segmentLitres"]) == 4 and g["samples"] <= 1 and not g["calibrated"], (s, g))
s, g = call("PUT", f"/api/vehicles/{vid}/gauge/manual", {"segmentLitres": [8, 10, 11, 14]}, token=tok)
check("gauge by hand: used at once", s == 200 and g["manual"] and g["segmentLitres"] == [8, 10, 11, 14], (s, g))
s, g = call("PUT", f"/api/vehicles/{vid}/gauge/manual", {"segmentLitres": [8, 10]}, token=tok)
check("gauge by hand: one value per segment", s == 422, (s, g))
s, g = call("PUT", f"/api/vehicles/{vid}/gauge/manual", {"segmentLitres": None}, token=tok)
check("gauge by hand cleared: learned again", s == 200 and not g["manual"], (s, g))
s, _ = call("PUT", f"/api/vehicles/{vid}/gauge/manual", {"segmentLitres": [8, 10, 11, 14]}, token=ptok)
check("gauge by hand: another user's vehicle is 404", s == 404, s)
s, _ = call("POST", f"/api/vehicles/{vid}/gauge/reset", token=tok)
check("gauge calibration reset", s == 200, s)
s, _ = call("GET", "/api/vehicles/00000000-0000-0000-0000-000000000000/gauge", token=tok)
check("gauge of a foreign vehicle is 404", s == 404, s)
s, r = call("GET", f"/api/settings/fuelPrices/test?profile={profiles[1]['id']}", token=tok)
check("admin: test connection", s == 200 and r["ok"] and r["stations"] == 2, (s, r))

# ---- price watch: favourite station, limit, Gotify (fake on the same port) ----
s, r_st = call("GET", f"/api/vehicles/{vid}/stations?lat=50.1&lng=8.76", token=tok)
aral, free = r_st["stations"][0], r_st["stations"][1]
watch = {"profileId": r_st["profile"], "stationId": aral["id"], "name": aral["brand"], "address": aral["address"],
         "lat": aral["lat"], "lng": aral["lng"], "grade": "e10", "lastPrice": aral["price"]}
s, w = call("POST", "/api/me/priceWatches", watch, token=tok)
check("watch a station (today's price is the first history point)", s == 200 and w["kind"] == "custom" and w["lastPrice"] == 1.739, (s, w))
s, _ = call("POST", "/api/me/priceWatches", watch, token=tok)
check("same station and grade twice refused", s == 422, s)
s, w2 = call("POST", "/api/me/priceWatches", {**watch, "grade": "diesel", "lastPrice": aral["prices"].get("diesel", 1.629)}, token=tok)
check("same station, another grade: its own watch", s == 200 and w2["grade"] == "diesel" and w2["id"] != w["id"], (s, w2))
s, ws = call("POST", "/api/me/priceWatches/check", token=tok)
by = {x["grade"]: x for x in ws}
check("check now: both grades of the station priced (one request per station)", s == 200 and by["e10"]["lastPrice"] == 1.739
      and by["diesel"]["lastPrice"] == 1.629 and not by["e10"]["lastError"] and not by["diesel"]["lastError"], (s, ws))
s, _ = call("PUT", f"/api/me/priceWatches/{w2['id']}", {"grade": "e10", "below": None}, token=tok)
check("changing a grade to one already watched there refused", s == 422, s)
s, _ = call("DELETE", f"/api/me/priceWatches/{w2['id']}", token=tok)
check("one grade of a station deleted", s == 200, s)
s, r = call("GET", f"/api/vehicles/{vid}/stations?lat=50.1&lng=8.76&grades=all", token=tok)
check("stations nearby with all grades (Price watch)", s == 200 and "diesel" in r["grades"] and "plus" in r["grades"]
      and r["stations"][0]["prices"].get("diesel") == 1.629 and r["stations"][0]["prices"].get("e5") == 1.799, (s, r))
s, _ = call("POST", "/api/me/priceWatches", {**watch, "stationId": free["id"], "lat": 0, "lng": 0}, token=tok)
check("station without a position refused", s == 422, s)
s, _ = call("POST", "/api/me/priceWatches", {**watch, "profileId": "nope"}, token=tok)
check("unknown price profile refused", s == 422, s)
s, ws = call("GET", "/api/me/priceWatches", token=ptok)
check("others don't see my watches", s == 200 and ws == [], (s, ws))
s, _ = call("PUT", f"/api/me/priceWatches/{w['id']}", {"grade": "e10", "below": 9}, token=ptok)
check("others can't change my watch", s == 422, s)
s, st = call("POST", "/api/me/priceWatchSettings", {"intervals": {"custom": 30, "goriva": 2880}}, token=tok)
check("intervals saved", s == 200 and st["intervals"]["custom"] == 30 and st["intervals"]["goriva"] == 2880
      and st["intervals"]["tankerkoenig"] == 60 and st["channels"] == 0, (s, st))
s, _ = call("POST", "/api/me/priceWatchSettings", {"intervals": {"custom": 5}}, token=tok)
check("interval under 15 minutes refused", s == 422, s)
# notification channels: Gotify (alerts below), then ntfy and Matrix
s, _ = call("POST", "/api/me/notifications", {"kind": "gotify", "url": "http://127.0.0.1:3999/", "enabled": True}, token=tok)
check("channel: Gotify needs its token", s == 422, s)
s, gch = call("POST", "/api/me/notifications", {"kind": "gotify", "name": "Phone", "url": "http://127.0.0.1:3999/", "secret": "ci-gotify",
                                                "enabled": True}, token=tok)
check("channel: Gotify added; token not echoed", s == 201 and gch["secretSet"] and gch["url"] == "http://127.0.0.1:3999"
      and gch["name"] == "Phone" and "ci-gotify" not in json.dumps(gch), (s, gch))
s, r = call("POST", f"/api/me/notifications/{gch['id']}/test", token=tok)
check("channel: Gotify test message", s == 200 and r["ok"], (s, r))
s, r = call("POST", f"/api/me/notifications/{gch['id']}/test", token=ptok)
s2, others = call("GET", "/api/me/notifications", token=ptok)
check("channel: another user can't see or use mine", s == 200 and not r["ok"] and others == [], (s, r, others))
sent_before = len(json.loads(urllib.request.urlopen("http://127.0.0.1:3999/messages", timeout=5).read()))
s, w = call("PUT", f"/api/me/priceWatches/{w['id']}", {"grade": "e10", "below": 1.80}, token=tok)
check("set a price limit", s == 200 and w["below"] == 1.8 and w["alerted"] is False, (s, w))
s, ws = call("POST", "/api/me/priceWatches/check", token=tok)
check("check now: price at or below the limit", s == 200 and ws[0]["alerted"] and ws[0]["lastPrice"] == 1.739 and not ws[0]["lastError"], (s, ws))
call("POST", "/api/me/priceWatches/check", token=tok)
msgs = json.loads(urllib.request.urlopen("http://127.0.0.1:3999/messages", timeout=5).read())[sent_before:]
alerts = [m for m in msgs if m["title"].startswith("⛽")]
check("one Gotify alert, not one per check", len(alerts) == 1 and "1.739" in alerts[0]["message"] and "1.800" in alerts[0]["message"], msgs)
click = (((alerts[0].get("extras") or {}).get("client::notification") or {}).get("click") or {}).get("url", "") if alerts else ""
check("alert: short title with grade, map link on tap", alerts and alerts[0]["title"].endswith("· Super E10")
      and click.startswith("https://www.google.com/maps/dir/?api=1&destination=50.103100,8.760100"), alerts)
s, h = call("GET", f"/api/me/priceWatches/{w['id']}/history?days=7", token=tok)
check("price history: one point (unchanged price isn't stored again)", s == 200 and [p["price"] for p in h["prices"]] == [1.739] and h["fillups"] == [], (s, h))
today = time.strftime("%Y-%m-%d")
s, h = call("GET", f"/api/me/priceWatches/{w['id']}/history?from=2020-01-01&to=2020-12-31", token=tok)
s2, h2 = call("GET", f"/api/me/priceWatches/{w['id']}/history?from={today}&to={today}", token=tok)
check("price history: a custom range (from/to)", s == 200 and h["prices"] == [] and s2 == 200 and [p["price"] for p in h2["prices"]] == [1.739], (h, h2))
s, w = call("PUT", f"/api/me/priceWatches/{w['id']}", {"grade": "e5", "below": None}, token=tok)
check("new grade starts a new history", s == 200 and w["lastPrice"] is None, (s, w))
s, ws = call("POST", "/api/me/priceWatches/check", token=tok)
check("check now: E5 price, no limit = no alert", s == 200 and ws[0]["lastPrice"] == 1.799 and not ws[0]["alerted"], (s, ws))
s, _ = call("DELETE", f"/api/me/priceWatches/{w['id']}", token=ptok)
check("others can't delete my watch", s == 404, s)
s, _ = call("DELETE", f"/api/me/priceWatches/{w['id']}", token=tok)
s2, ws = call("GET", "/api/me/priceWatches", token=tok)
check("watch deleted", s == 200 and ws == [], (s, ws))
# ntfy (JSON publish: titles with ⛽ aren't valid in headers) and Matrix channels
s, _ = call("POST", "/api/me/notifications", {"kind": "ntfy", "url": "http://127.0.0.1:3999", "enabled": True}, token=tok)
check("ntfy: the topic URL needs a topic", s == 422, s)
s, nch = call("POST", "/api/me/notifications", {"kind": "ntfy", "url": "http://127.0.0.1:3999/ntfy/hammond-ci", "secret": "ci-ntfy",
                                                "enabled": True}, token=tok)
s2, r = call("POST", f"/api/me/notifications/{nch['id']}/test", token=tok)
msgs = json.loads(urllib.request.urlopen("http://127.0.0.1:3999/messages", timeout=5).read())
check("ntfy: test message to the topic, token kept secret", s == 201 and nch["secretSet"] and "ci-ntfy" not in json.dumps(nch) and r["ok"]
      and msgs[-1].get("via") == "ntfy" and msgs[-1]["topic"] == "hammond-ci" and msgs[-1]["title"] == "hammond", (s, nch, r, msgs[-1:]))
s, mch = call("POST", "/api/me/notifications", {"kind": "matrix", "url": "http://127.0.0.1:3999", "room": "#fuel:ci", "secret": "ci-matrix",
                                                "enabled": True}, token=tok)
s2, r = call("POST", f"/api/me/notifications/{mch['id']}/test", token=tok)
msgs = json.loads(urllib.request.urlopen("http://127.0.0.1:3999/messages", timeout=5).read())
check("matrix: alias resolved, message posted to the room", s == 201 and r["ok"] and msgs[-1].get("via") == "matrix"
      and msgs[-1]["room"] == "!fuel:ci" and msgs[-1]["formatted_body"].startswith("<b>hammond</b>"), (s, mch, r, msgs[-1:]))
# a Matrix channel marked as homebot's Fuel room also gets "fill-up detected" (answer there with the price)
s, mf = call("POST", "/api/me/notifications", {"kind": "matrix", "url": "http://127.0.0.1:3999", "room": "!fuel:ci", "secret": "ci-matrix",
                                               "enabled": True, "commands": True}, token=tok)
s2, k3 = call("POST", "/api/me/apikeys", {"name": "det"}, token=tok)
before = len(json.loads(urllib.request.urlopen("http://127.0.0.1:3999/messages", timeout=5).read()))
s3, d = call("POST", f"/api/v1/vehicles/{vid}/detections", {"odoReading": 12900, "fuelQuantity": 33, "source": "Home Assistant"}, token=k3["key"])
time.sleep(1)  # sent in the background
msgs = json.loads(urllib.request.urlopen("http://127.0.0.1:3999/messages", timeout=5).read())[before:]
det = [m for m in msgs if m.get("via") == "matrix" and "Fill-up detected" in m.get("body", "")]
check("matrix Fuel room: fill-up detected, answer with the price", s == 201 and mf["commands"] and s3 == 201 and len(det) == 1
      and "Answer with the price" in det[0]["body"] and det[0]["room"] == "!fuel:ci", (s, mf.get("commands"), s3, msgs))
call("POST", f"/api/detections/{d['detection']['id']}/discard", token=tok)
call("DELETE", f"/api/me/apikeys/{k3['apiKey']['id']}", token=tok)
call("DELETE", f"/api/me/notifications/{mf['id']}", token=tok)
# edit: off + new name keeps the token; delete
s, e = call("PUT", f"/api/me/notifications/{mch['id']}", {**mch, "enabled": False, "name": "Fuel room", "secret": ""}, token=tok)
s2, lst = call("GET", "/api/me/notifications", token=tok)
check("channel: turned off and renamed, token kept", s == 200 and not e["enabled"] and e["name"] == "Fuel room" and e["secretSet"]
      and [c["kind"] for c in lst] == ["gotify", "ntfy", "matrix"], (s, e, lst))
s, st = call("GET", "/api/me/priceWatchSettings", token=tok)
check("price watch shows how many channels are on", s == 200 and st["channels"] == 2, st)
for c in lst:
    call("DELETE", f"/api/me/notifications/{c['id']}", token=tok)
s, lst = call("GET", "/api/me/notifications", token=tok)
check("channels deleted", s == 200 and lst == [], lst)
call("POST", "/api/me/priceWatchSettings", {"intervals": {}}, token=tok)
s, _ = call("PUT", f"/api/vehicles/{vid}", {"nickname": "Smoke", "registration": "LJ SMOKE1", "make": "Skoda", "model": "Octavia",
     "fuelUnit": 0, "fuelType": 1, "tankCapacity": 50, "fuelGrade": "e10,hbuzg"}, token=tok)
check("unknown fuel grade refused", s == 422, s)

# ---- Home Assistant odometer (fake HA on the same port) ----
s, r = call("GET", f"/api/vehicles/{vid}/odometer", token=tok)
check("odometer: not configured without HA", s == 200 and r["configured"] is False, (s, r))
s, st = call("POST", "/api/me/homeAssistant", {"url": "http://127.0.0.1:3999", "token": "ci-ha-token"}, token=tok)
check("user sets their own HA; token not echoed", s == 200 and st.get("haTokenSet") is True and "ci-ha-token" not in json.dumps(st), (s, st))
s, me = call("GET", "/api/me", token=tok)
check("/me shows HA without the token", s == 200 and me.get("haUrl") == "http://127.0.0.1:3999" and me.get("haTokenSet") is True
      and "ci-ha-token" not in json.dumps(me), me)
s, r = call("GET", "/api/me/homeAssistant/test", token=tok)
check("HA test connection", s == 200 and r["ok"] and r["name"] == "CI Home", (s, r))
s, r = call("GET", "/api/me/homeAssistant/test", token=ptok)
check("another user's HA isn't shared", s == 200 and not r["ok"], (s, r))
s, r = call("GET", "/api/me/homeAssistant/entity?entity=sensor.ci_odometer", token=tok)
check("check an entity before saving it", s == 200 and r["value"] == 23456 and r["unit"] == "km", (s, r))
s, r = call("GET", "/api/me/homeAssistant/entity?entity=sensor.nope", token=tok)
check("check: a missing entity is a clear error", s == 422 and "no entity" in r, (s, r))
veh = {"nickname": "Smoke", "registration": "LJ SMOKE1", "make": "Skoda", "model": "Octavia", "fuelUnit": 0, "fuelType": 1,
       "tankCapacity": 50, "fuelGrade": "e10"}
s, _ = call("PUT", f"/api/vehicles/{vid}", {**veh, "odometerEntity": "../../api/config"}, token=tok)
check("odometer entity must be an entity id", s == 422, s)
call("PUT", f"/api/vehicles/{vid}", {**veh, "odometerEntity": "sensor.ci_odometer"}, token=tok)
s, r = call("GET", f"/api/vehicles/{vid}/odometer", token=tok)
check("odometer from HA", s == 200 and r["configured"] and r["value"] == 23456 and r["unit"] == "km", (s, r))

# ---- trips: drives from HA history (fake: 2 drives 4 h ago, 5 min stop, 40 + 35 km, tank 50 l: 5 + 4 l) ----
s, r = call("PUT", f"/api/vehicles/{vid}/tripEntities", {"ignition": "sensor.ci_ignition", "fuelLevel": "sensor.ci_fuel"}, token=tok)
check("trip entities saved", s == 200 and r["ignition"] == "sensor.ci_ignition", (s, r))
s, r = call("PUT", f"/api/vehicles/{vid}/tripEntities", {"ignition": "../api/config"}, token=tok)
check("trip entity must be an entity id", s == 422, (s, r))
call("PUT", f"/api/vehicles/{vid}/tripEntities", {"ignition": "sensor.ci_ignition", "fuelLevel": "sensor.ci_fuel",
                                                   "phoneLocation": "device_tracker.ci_phone"}, token=tok)
s, r = call("POST", f"/api/vehicles/{vid}/trips/sync", token=tok)
check("trips: sync reads 2 drives from HA", s == 200 and r["added"] == 2, (s, r))
s, r = call("POST", f"/api/vehicles/{vid}/trips/sync", token=tok)
check("trips: a second sync adds nothing", s == 200 and r["added"] == 0, (s, r))
s, trips = call("GET", f"/api/vehicles/{vid}/trips", token=tok)
t = trips[0] if s == 200 and trips else {}
check("trips: the 5 min stop joins both drives into one trip", s == 200 and len(trips) == 1 and len(t["legs"]) == 2
      and t["distance"] == 75 and t["startOdo"] == 1000 and t["endOdo"] == 1075 and abs(t["fuelUsed"] - 9) < 1e-6
      and t["driveMinutes"] == 55, (s, trips))
legs = t.get("legs") or [{}, {}]
check("trips: start/end positions and the route from the phone", legs[0].get("locationFrom") == "phone"
      and legs[0].get("startLat") == 50.079 and legs[1].get("endLat") == 50.11 and len(legs[0].get("route") or []) == 4
      and len(legs[1].get("route") or []) == 4, legs)
s, _ = call("GET", f"/api/vehicles/{vid}/trips", token=ptok)
check("trips: another user's vehicle is 404", s == 404, s)
s, _ = call("PUT", f"/api/trips/{t.get('id')}", {"business": True, "purpose": "Customer A, Frankfurt", "notes": "contract"}, token=tok)
s2, _ = call("POST", f"/api/trips/{t.get('id')}/costs", {"amount": 12.5, "type": "Toll", "comment": "A3"}, token=tok)
s3, _ = call("PUT", f"/api/trips/{t.get('id')}", {"purpose": "x"}, token=ptok)
s, trips = call("GET", f"/api/vehicles/{vid}/trips", token=tok)
t = trips[0]
check("trips: purpose, business, a toll as trip cost; others can't edit", s2 == 201 and s3 == 404 and t["business"]
      and t["purpose"] == "Customer A, Frankfurt" and t["extraCost"] == 12.5 and t["costs"][0]["expenseType"] == "Toll", (s2, s3, t))
s, r = call("POST", f"/api/legs/{t['legs'][1]['id']}/split", token=tok)
s2, trips = call("GET", f"/api/vehicles/{vid}/trips", token=tok)
check("trips: split at the second drive = 2 trips", s == 200 and len(trips) == 2, (s, r, trips))
s, r = call("POST", f"/api/trips/{trips[0]['id']}/join", token=tok)
s2, trips = call("GET", f"/api/vehicles/{vid}/trips", token=tok)
check("trips: join again = 1 trip, purpose and cost kept", s == 200 and len(trips) == 1 and trips[0]["purpose"] == "Customer A, Frankfurt"
      and trips[0]["extraCost"] == 12.5, (s, r, trips))
# the chat on a real trip: purpose / private, and a cost lands on today's trip
s, k2 = call("POST", "/api/me/apikeys", {"name": "chat"}, token=tok)
cmd2 = lambda text: call("POST", "/api/v1/command", {"text": text}, token=k2["key"])[1]["text"]
r = cmd2("purpose Kunde Müller (Vertrag)")
r2 = cmd2("private")
s, tr = call("GET", f"/api/vehicles/{vid}/trips", token=tok)
check("chat: purpose and private on the latest trip", r.startswith("**✓ Trip") and "📝 Kunde Müller (Vertrag)" in r and "private ·" in r2
      and tr[0]["purpose"] == "Kunde Müller (Vertrag)" and tr[0]["business"] is False, (r, r2, tr[0]["purpose"], tr[0]["business"]))
today_trip = tr[0]["start"][:10] == time.strftime("%Y-%m-%d", time.gmtime())  # the fake drives are 4 h ago: yesterday just after midnight
r = cmd2("cost toll 4.20 A5")
s, tr = call("GET", f"/api/vehicles/{vid}/trips", token=tok)
check("chat: a cost lands on today's trip", (r.startswith("**✓ Toll 4.20 on today's trip") and abs(tr[0]["extraCost"] - (12.5 + 4.2)) < 1e-3)  # float32 amounts: 16.6999998
      if today_trip else r.startswith("**✓ Toll 4.20 saved"), (today_trip, r, tr[0]["extraCost"]))
cmd2("undo")
cmd2("business")
cmd2("purpose Customer A, Frankfurt")  # back to what the checks below expect
call("DELETE", f"/api/me/apikeys/{k2['apiKey']['id']}", token=tok)
s, csv_text = call("GET", f"/api/vehicles/{vid}/trips.csv", token=tok, raw=True)
check("trips: CSV export", s == 200 and "Customer A" in str(csv_text) and "Toll 12.50 A3" in str(csv_text), (s, csv_text))
s, _ = call("DELETE", f"/api/trips/{trips[0]['id']}", token=tok)
s2, r = call("POST", f"/api/vehicles/{vid}/trips/sync", token=tok)
s3, left = call("GET", f"/api/vehicles/{vid}/trips", token=tok)
check("trips: a deleted trip stays deleted after the next sync", s == 200 and s2 == 200 and r["added"] == 0 and left == [], (s, s2, r, left))
s, _ = call("PUT", f"/api/vehicles/{vid}/tripsPaused", {"paused": True}, token=tok)
s2, r = call("POST", f"/api/vehicles/{vid}/trips/sync", token=tok)
s3, _ = call("PUT", f"/api/vehicles/{vid}/tripsPaused", {"paused": False}, token=tok)
check("trips: recording paused (no sync, also not Sync now), then on again", s == 200 and s2 == 422 and "paused" in str(r)
      and s3 == 200 and call("GET", f"/api/vehicles/{vid}", token=tok)[1].get("tripsPaused") is False, (s, s2, r, s3))
s, r = call("POST", "/api/settings/trips", {"locked": True}, token=tok)
s2, st = call("GET", "/api/settings", token=tok)
s3, _ = call("POST", "/api/settings/trips", {"locked": False}, token=ptok)
check("trips: admin locks times/km (users can't)", s == 200 and st.get("tripsLocked") is True and s3 == 401, (s, st, s3))
call("POST", "/api/settings/trips", {"locked": False}, token=tok)
call("PUT", f"/api/vehicles/{vid}/tripEntities", {}, token=tok)

# ---- MQTT → Home Assistant discovery (fake broker on 127.0.0.1:4000, user "ci-mqtt") ----
fake_mqtt = lambda: json.loads(urllib.request.urlopen("http://127.0.0.1:3999/mqtt", timeout=5).read())
s, _ = call("POST", "/api/me/mqtt", {"url": "http://127.0.0.1:4000"}, token=tok)
check("mqtt: only broker URLs", s == 422, s)
s, u = call("POST", "/api/me/mqtt", {"url": "mqtt://127.0.0.1:4000", "user": "wrong", "password": "ci-pw"}, token=tok)
s2, r = call("POST", "/api/me/mqtt/publish", token=tok)
check("mqtt: a refused login is a clear error", s == 200 and s2 == 422 and "MQTT broker" in str(r), (s, s2, r))
s, u = call("POST", "/api/me/mqtt", {"url": "mqtt://127.0.0.1:4000", "user": "ci-mqtt", "password": " ci-pw\n"}, token=tok)  # pasted with a line break
check("mqtt: settings saved, password kept and never shown", s == 200 and u["mqttUser"] == "ci-mqtt" and u["mqttPasswordSet"] is True
      and "ci-pw" not in json.dumps(u), (s, u))
s, r = call("POST", "/api/me/mqtt/publish", token=tok)
m = fake_mqtt()
base = "hammond/" + me["id"][:8]
cfg = json.loads(m.get(f"homeassistant/sensor/hammond_{vid}/odometer/config", "{}"))
state = json.loads(m.get(f"{base}/vehicle/{vid}", "{}"))
check("mqtt: HA discovery + state for the vehicle", s == 200 and r["vehicles"] == 1 and cfg.get("unique_id") == f"hammond_{vid}_odometer"
      and cfg.get("device", {}).get("name") == "hammond Smoke" and cfg.get("state_topic") == f"{base}/vehicle/{vid}"
      and cfg.get("unit_of_measurement") == "km" and m.get(f"{base}/status") == "online" and state.get("odometer", 0) > 0
      and "month_total" in state and f"homeassistant/sensor/hammond_{vid}/month_total/config" in m, (s, r, cfg, state, sorted(m)[:5]))
# a restart: the old connection's last will ("offline", retained) can reach the broker after the new "online";
# the next publish must say "online" again, or HA shows every entity unavailable until a reconnect
urllib.request.urlopen(urllib.request.Request("http://127.0.0.1:3999/mqtt/set", method="POST",
                       data=json.dumps({"topic": f"{base}/status", "payload": "offline"}).encode()), timeout=5)
s, _ = call("POST", "/api/me/mqtt/publish", token=tok)
img = json.loads(fake_mqtt().get(f"homeassistant/image/hammond_{vid}/photo/config", "{}"))
odo_cfg = json.loads(fake_mqtt().get(f"homeassistant/sensor/hammond_{vid}/odometer/config", "{}"))
check("mqtt: sensors keep their icons (no car photo as entity_picture)", "entity_picture" not in odo_cfg, odo_cfg.get("entity_picture"))
check("mqtt: car sensors carry only their own value (no copy of every value as attributes)", "json_attributes_topic" not in odo_cfg, odo_cfg.get("json_attributes_topic"))
check("mqtt: the car photo as an image entity (absolute link from the upload's origin)",
      img.get("url_topic") == f"{base}/vehicle/{vid}/photo" and fake_mqtt().get(f"{base}/vehicle/{vid}/photo", "").startswith("https://fuel.example.com/api/public/photo/"), img)
check("mqtt: a late last will (offline) is overwritten by the next publish", s == 200 and fake_mqtt().get(f"{base}/status") == "online",
      fake_mqtt().get(f"{base}/status"))
# Trips hidden in the user's settings: trip sensors leave HA, the chat says so; then shown again
call("POST", "/api/me/settings", {**base_settings, "showTrips": False}, token=tok)
s, _ = call("POST", "/api/me/mqtt/publish", token=tok)
trip_cfg = fake_mqtt().get(f"homeassistant/sensor/hammond_{vid}/trips_today_distance/config")
s2, k3 = call("POST", "/api/me/apikeys", {"name": "trips off"}, token=tok)
off = call("POST", "/api/v1/command", {"text": "trips"}, token=k3["key"])[1]["text"]
call("DELETE", f"/api/me/apikeys/{k3['apiKey']['id']}", token=tok)
check("Show Trips off: saved, trip sensors removed from HA, chat says trips are off", s == 200 and trip_cfg == ""
      and call("GET", "/api/me", token=tok)[1].get("showTrips") is False and off.startswith("Trips are off"), (s, trip_cfg, off))
call("POST", "/api/me/settings", {**base_settings, "showTrips": True}, token=tok)
s, _ = call("POST", "/api/me/mqtt/publish", token=tok)
check("Show Trips on again: trip sensors back", json.loads(fake_mqtt().get(f"homeassistant/sensor/hammond_{vid}/trips_today_distance/config") or "{}").get("name") == "Trips today", s)
mt = json.loads(m.get(f"homeassistant/sensor/hammond_{vid}/month_total/config", "{}"))
l30 = json.loads(m.get(f"homeassistant/sensor/hammond_{vid}/last30_total/config", "{}"))
check("mqtt: last 30 days + this year; monthly totals tell HA when they start over",
      "last30_total" in state and "last30_fillups" in state and "year_total" in state and state.get("month_start", "").endswith(("Z", ":00"))
      and mt.get("last_reset_value_template") == "{{ value_json.month_start }}" and l30.get("name") == "Costs last 30 days"
      and "last_reset_value_template" not in l30, (state, mt, l30))
s, _ = call("POST", "/api/me/mqtt", {"url": ""}, token=tok)
check("mqtt: turned off = HA shows it offline", s == 200 and fake_mqtt().get(f"{base}/status") == "offline", fake_mqtt().get(f"{base}/status"))

call("PUT", f"/api/vehicles/{vid}", veh, token=tok)
call("POST", "/api/me/homeAssistant", {"url": "", "token": "-"}, token=tok)

# ---- EV charging: places, tariff quote, EVCC import (fake EVCC on :3999) ----
s, ev = call("POST", "/api/vehicles", {"nickname": "CI EV", "registration": "LJ EV1", "make": "VW", "model": "ID.3",
             "fuelUnit": 4, "fuelType": 4, "evccVehicle": "CI EV"}, token=tok)
check("electric vehicle with EVCC name", s in (200, 201) and ev.get("evccVehicle") == "CI EV", (s, ev))
place = {"name": "Home", "lossPercent": 0, "sessionFee": 0.5, "components": [
    {"name": "Energy", "price": 0.10, "rules": [{"label": "NT", "days": "all", "from": "22:00", "to": "06:00", "price": 0.05}]}]}
s, mine = call("POST", "/api/chargingPlaces", {**place, "name": "Plain's home", "shared": True}, token=ptok)
check("users create their own place, never shared", s == 200 and mine["shared"] is False, (s, mine))
s, ps = call("GET", "/api/chargingPlaces", token=tok)
check("others' private places are not listed", s == 200 and all(p["id"] != mine["id"] for p in ps), (s, ps))
call("DELETE", f"/api/chargingPlaces/{mine['id']}", token=ptok)
s, _ = call("POST", "/api/chargingPlaces", {**place, "components": [{"rules": [{"from": "25:00"}]}]}, token=tok)
check("invalid tariff rule refused", s == 422, s)
s, home = call("POST", "/api/chargingPlaces", {**place, "shared": True}, token=tok)
check("admin creates a shared charging place", s == 200 and home.get("id") and home["shared"] and home["components"][0]["rules"][0]["label"] == "NT", (s, home))
s, _ = call("PUT", f"/api/chargingPlaces/{home['id']}", {**place, "name": "Hacked"}, token=ptok)
check("users can't change shared places", s == 422, s)
s, q = call("POST", f"/api/chargingPlaces/{home['id']}/quote", {"start": "2026-11-10T21:00:00Z", "end": "2026-11-10T23:00:00Z", "kWh": 10}, token=ptok)
check("quote: half day, half night tariff + session fee", s == 200 and abs(q["total"] - 1.25) < 0.001 and len(q["parts"]) == 2, (s, q))
s, ps = call("GET", "/api/chargingPlaces", token=ptok)
check("users see shared places", s == 200 and len(ps) == 1, (s, ps))
s, st = call("POST", "/api/me/evcc", {"url": "http://127.0.0.1:3999", "syncMinutes": 0, "placeId": home["id"],
             "since": "2026-01-01T00:00:00Z"}, token=tok)
check("user sets their EVCC", s == 200 and st.get("evccPlaceId") == home["id"], (s, st))
s, r = call("POST", "/api/me/evcc/sync", token=ptok)
check("another user's EVCC isn't shared", s == 422, (s, r))
s, r = call("POST", "/api/me/evcc/sync", token=tok)
check("EVCC sync imports the session", s == 200 and r["imported"] == 1, (s, r))
s, r = call("POST", "/api/me/evcc/sync", token=tok)
check("EVCC sync again: nothing new", s == 200 and r["imported"] == 0, (s, r))
s, dets = call("GET", "/api/detections", token=tok)
evd = [d for d in dets if d.get("source") == "EVCC"] if s == 200 else []
check("EVCC detection priced with the place (night 0.05 + fee 0.5 / 12 kWh)", len(evd) == 1 and evd[0]["station"] == "Home"
      and abs(evd[0]["perUnitPrice"] - round((12 * 0.05 + 0.5) / 12, 4)) < 0.0001 and evd[0]["odoReading"] == 0, evd)
for d in evd:
    call("POST", f"/api/detections/{d['id']}/discard", token=tok)
call("POST", "/api/me/evcc", {"url": "", "syncMinutes": 0, "placeId": ""}, token=tok)
call("DELETE", f"/api/chargingPlaces/{home['id']}", token=tok)
call("DELETE", f"/api/vehicles/{ev['id']}", token=tok)

s, _ = call("POST", "/api/me/settings", {**{"currency": "EUR", "distanceUnit": 1}, "stationRadiusKm": 10}, token=ptok)
s2, me2 = call("GET", "/api/me", token=ptok)
check("station search radius is a user setting", s == 200 and me2.get("stationRadiusKm") == 10, (s, me2))
s, _ = call("POST", "/api/me/settings", {"currency": "EUR", "distanceUnit": 1, "stationRadiusKm": 30}, token=ptok)
check("radius over 25 km refused", s == 422, s)

s, ref = call("POST", "/api/refresh", {"refreshToken": login["refreshToken"]}, token=tok)
check("refresh token", s == 200 and ref.get("token"), (s, ref))
s, _ = call("POST", "/api/refresh", {"refreshToken": "garbage"}, token=tok)
check("garbage refresh token is 401 (no crash)", s == 401, s)

print("all smoke tests passed")
