"""Fill an EMPTY hammond with made-up demo data, for screenshots and trying it out.

    python3 tests/fake_fuelprices.py 3999 &          # fake station prices (CI fake)
    CONFIG=/tmp/demo/config DATA=/tmp/demo/data ./app &
    python3 scripts/demo_data.py http://127.0.0.1:3000 /tmp/demo/config/hammond.db [photo.png]

Everything is invented: user demo@example.com / demo-password, a VW Golf "DEMO 123", 14 months of fill-ups
and expenses, two watched stations with 90 days of prices, trips with routes in Frankfurt's city centre, a
gauge calibration and (optionally) a car photo. Price history and trip routes have no API (they come from
background checks and Home Assistant), so those are written into the database directly.
"""
import json
import math
import os
import random
import sqlite3
import sys
import urllib.error
import urllib.request
from datetime import datetime, timedelta, timezone

BASE = sys.argv[1].rstrip("/")
DB = sys.argv[2]
PHOTO = sys.argv[3] if len(sys.argv) > 3 else ""
PRICES = os.environ.get("DEMO_PRICES", "http://127.0.0.1:3999/json")  # tests/fake_fuelprices.py
random.seed(7)  # the same demo every time
now = datetime.now(timezone.utc).replace(microsecond=0)
iso = lambda d: d.strftime("%Y-%m-%dT%H:%M:%SZ")


def call(method, path, body=None, token=None):
    req = urllib.request.Request(BASE + path, method=method, data=json.dumps(body).encode() if body is not None else None)
    req.add_header("Content-Type", "application/json")
    if token:
        req.add_header("Authorization", "Bearer " + token)
    try:
        with urllib.request.urlopen(req, timeout=60) as r:
            data = r.read()
            return json.loads(data) if data else None
    except urllib.error.HTTPError as e:
        sys.exit(f"{method} {path}: HTTP {e.code} {e.read().decode(errors='replace')[:300]}")


call("POST", "/api/auth/initialize", {"name": "Demo", "email": "demo@example.com", "password": "demo-password",
                                      "currency": "EUR", "distanceUnit": 1})
tok = call("POST", "/api/login", {"email": "demo@example.com", "password": "demo-password"})["token"]
me = call("GET", "/api/me", token=tok)
call("POST", "/api/me/settings", {"currency": "EUR", "distanceUnit": 1, "dateFormat": "dd/MM/yyyy", "theme": "auto",
                                  "prefillFillup": True, "showFillupTime": True}, token=tok)

v = call("POST", "/api/vehicles", {"nickname": "Golf", "registration": "DEMO 123", "make": "Volkswagen", "model": "Golf 1.5 TSI",
                                   "yearOfManufacture": 2019, "engineSize": 1.5, "fuelUnit": 0, "fuelType": 0}, token=tok)
vid = v["id"]
call("PUT", f"/api/vehicles/{vid}", {"nickname": "Golf", "registration": "DEMO 123", "make": "Volkswagen", "model": "Golf 1.5 TSI",
                                     "yearOfManufacture": 2019, "engineSize": 1.5, "fuelUnit": 0, "fuelType": 0,
                                     "tankCapacity": 50, "fuelGrade": "e10,e5"}, token=tok)

# ---- 14 months of fill-ups: ~550–750 km apart, prices that drift like real ones ----
stations = ["ARAL, Frankfurter Str. 12, 63067 Offenbach", "Freie Tankstelle, Hafen, 63067 Offenbach",
            "Shell, Mainzer Landstraße 80, 60327 Frankfurt", "JET, Hanauer Landstraße 200, 60314 Frankfurt"]
odo, day = 41200, now - timedelta(days=425)
while True:
    km = random.randint(550, 750)
    day += timedelta(days=random.randint(16, 30), hours=random.randint(0, 9))
    if day > now - timedelta(days=2):  # no fill-ups in the future
        break
    odo += km
    litres = round(km * random.uniform(5.8, 7.4) / 100, 2)
    price = round(1.72 + 0.09 * math.sin(day.timetuple().tm_yday / 58) + random.uniform(-0.03, 0.03), 3)
    call("POST", f"/api/vehicles/{vid}/fillups", {
        "vehicleId": vid, "fuelUnit": 0, "fuelQuantity": litres, "perUnitPrice": price, "totalAmount": round(litres * price, 2),
        "odoReading": odo, "isTankFull": True, "hasMissedFillup": False, "fillingStation": random.choice(stations),
        "fuelSubType": "95 E10", "userId": me["id"], "date": iso(day)}, token=tok)
for days_ago, kind, amount, comment in ((380, "Insurance", 512.40, "Full cover, year"), (300, "Service", 289.00, "Inspection + oil"),
                                        (210, "Tyres", 436.00, "Summer tyres"), (120, "Car wash", 14.90, ""),
                                        (60, "Parking", 18.00, "Airport"), (20, "Tyres", 60.00, "Winter tyres fitted")):
    call("POST", f"/api/vehicles/{vid}/expenses", {"vehicleId": vid, "amount": amount, "odoReading": odo - days_ago * 15,
                                                   "comments": comment, "expenseType": kind, "userId": me["id"],
                                                   "date": iso(now - timedelta(days=days_ago))}, token=tok)

# ---- gauge: litres per quarter (a gauge that reads "full" longer) ----
call("PUT", f"/api/vehicles/{vid}/gauge/manual", {"segmentLitres": [11, 12, 12.5, 14.5]}, token=tok)

# ---- price watch: a station of the (fake) price source, E10 and E5, 90 days of prices ----
call("POST", "/api/settings/fuelPrices", {"profiles": [{"name": "Demo prices", "kind": "custom", "url": PRICES, "apiKey": "ci-key"}]}, token=tok)
st = call("GET", f"/api/vehicles/{vid}/stations?lat=50.1&lng=8.76", token=tok)
watches = []
for s, grade in ((st["stations"][0], "e10"), (st["stations"][0], "e5")):  # one station, two grades: one card
    w = call("POST", "/api/me/priceWatches", {"profileId": st["profile"], "stationId": s["id"], "name": s["brand"] or s["name"],
                                              "address": s["address"], "lat": s["lat"], "lng": s["lng"], "grade": grade,
                                              "lastPrice": s["prices"].get(grade)}, token=tok)
    watches.append((w["id"], 1.74 if grade == "e10" else 1.80))
call("PUT", f"/api/me/priceWatches/{watches[0][0]}", {"grade": "e10", "below": 1.70}, token=tok)

db = sqlite3.connect(DB)
for wid, base in watches:
    db.execute("DELETE FROM station_prices WHERE watch_id = ?", (wid,))
    t = now - timedelta(days=90)
    while t < now:
        # cheap in the evening, up in the morning, a slow drift on top
        hour_cost = {6: 0.06, 7: 0.05, 12: 0.03, 17: 0.0, 20: -0.03, 22: -0.02}
        for h, add in hour_cost.items():
            at = t.replace(hour=h, minute=random.choice((0, 7, 23, 41)))
            p = round(base + 0.05 * math.sin(at.timetuple().tm_yday / 9) + add + random.uniform(-0.01, 0.01), 3)
            db.execute("INSERT INTO station_prices (watch_id, price, at) VALUES (?, ?, ?)", (wid, p, at.strftime("%Y-%m-%d %H:%M:%S+00:00")))
        t += timedelta(days=1)
db.commit()  # before the next API calls: hammond writes to the same SQLite file

# placeholder sensor names, as after the Trips setup (there's no Home Assistant behind the demo)
call("PUT", f"/api/vehicles/{vid}/tripEntities", {"ignition": "sensor.demo_ignition_state", "fuelLevel": "sensor.demo_fuel_level",
                                                  "phoneLocation": "device_tracker.demo_phone"}, token=tok)

# ---- trips: the last 10 days, routes in Frankfurt (made-up drives between public places) ----
places = {"Home": (50.1036, 8.7621, "Kaiserstraße 50, 63065 Offenbach am Main"),
          "Office": (50.1109, 8.6821, "Neue Mainzer Straße 52, 60311 Frankfurt am Main"),
          "Messe": (50.1121, 8.6511, "Ludwig-Erhard-Anlage 1, 60327 Frankfurt am Main"),
          "Airport": (50.0510, 8.5706, "Hugo-Eckener-Ring, 60549 Frankfurt am Main"),
          "Shop": (50.1186, 8.7380, "Hanauer Landstraße 450, 60314 Frankfurt am Main")}
plans = [("Home", "Office", True, "Customer meeting"), ("Office", "Messe", True, "Trade fair"), ("Messe", "Home", False, ""),
         ("Home", "Shop", False, "Shopping"), ("Home", "Airport", True, "Flight to Munich"), ("Airport", "Home", True, ""),
         ("Home", "Office", True, "Project kick-off"), ("Office", "Home", True, "")]
trip_odo, level = odo + 30, 78.0
for i, (a, b, business, purpose) in enumerate(plans):
    start = (now - timedelta(days=10 - i)).replace(hour=7 + (i % 3) * 3, minute=15)
    (la1, ln1, ad1), (la2, ln2, ad2) = places[a], places[b]
    straight = math.hypot((la2 - la1) * 111, (ln2 - ln1) * 71)
    km = round(straight * 1.35 + 1.5, 1)
    mins = int(km * 2.2 + 8)
    used = round(km * random.uniform(6.0, 8.5) / 100, 2)
    r = call("POST", f"/api/vehicles/{vid}/trips", {"start": iso(start), "end": iso(start + timedelta(minutes=mins)),
                                                    "startOdo": trip_odo, "endOdo": trip_odo + round(km), "distance": km,
                                                    "fuelUsed": used, "business": business, "purpose": purpose}, token=tok)
    leg = db.execute("SELECT id FROM trip_legs WHERE trip_id = ?", (r["id"],)).fetchone()[0]
    # a road-like route: points between both ends with a bend
    route = [[la1, ln1]] + [[la1 + (la2 - la1) * f + 0.004 * math.sin(f * math.pi), ln1 + (ln2 - ln1) * f] for f in (0.2, 0.4, 0.6, 0.8)] + [[la2, ln2]]
    fe = level - used / 50 * 100
    db.execute("""UPDATE trip_legs SET start_lat=?, start_lng=?, end_lat=?, end_lng=?, start_address=?, end_address=?, route=?,
                  location_from='phone', fuel_start=?, fuel_end=? WHERE id=?""",
               (la1, ln1, la2, ln2, ad1, ad2, json.dumps(route), round(level), round(fe), leg))
    db.commit()
    level, trip_odo = fe, trip_odo + round(km) + 3
db.commit()

if PHOTO:  # multipart upload, like the UI
    bnd = "----demo"
    body = (f"--{bnd}\r\nContent-Disposition: form-data; name=\"origin\"\r\n\r\n{BASE}\r\n--{bnd}\r\n"
            f"Content-Disposition: form-data; name=\"file\"; filename=\"car.png\"\r\nContent-Type: image/png\r\n\r\n").encode() \
        + open(PHOTO, "rb").read() + f"\r\n--{bnd}--\r\n".encode()
    req = urllib.request.Request(BASE + f"/api/vehicles/{vid}/photo", method="PUT", data=body)
    req.add_header("Content-Type", "multipart/form-data; boundary=" + bnd)
    req.add_header("Authorization", "Bearer " + tok)
    urllib.request.urlopen(req, timeout=60).read()

print(f"demo ready: {BASE}  demo@example.com / demo-password  vehicle {vid}")
