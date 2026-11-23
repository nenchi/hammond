# Home Assistant

Four things, each optional: the **odometer** pre-filled in new fill-ups, **detected fill-ups** from the fuel level,
hammond's numbers as **sensors over MQTT**, and the **trip log** ([Trips](trips.md)).

## Connect

1. In Home Assistant: your profile → *Security* → **Long-lived access tokens** → *Create token*.
2. In hammond: *Settings → Home Assistant*: the URL (`http://homeassistant.local:8123`) and the token, *Save*,
   **Test connection**. The token is never shown again; leave the field empty to keep it.
3. *Edit vehicle* → **Home Assistant odometer entity** (e.g. `sensor.my_car_odometer`) → **Check**. New fill-ups now
   start with the car's current odometer.

## Detected fill-ups

The car's fuel level jumps by 10 % or more → hammond gets a **pending fill-up** with the litres estimated from the
level difference (see [gauge calibration](gauge.md)); you only add the price.

1. hammond: an [API key](api-keys.md), the vehicle's **tank size**.
2. Home Assistant: the `rest_command`, two helpers and the blueprint
   [`hammond_fillup_detection.yaml`](../integrations/home-assistant/hammond_fillup_detection.yaml): step by step in
   the [main README](../README.md#automatic-fill-up-detection-home-assistant--api).
3. With the **phone's location** (HA companion app → *Manage sensors* → *Background location* on, location "allow
   all the time"), the detection takes the station you're at and its current price.

The detection uses the moment the level jumped (odometer, position, the car's own time), not the moment it's sent,
so driving off before it arrives doesn't count extra kilometres. Complete it in *Detected*, or answer the price in
[Telegram/Matrix](notifications.md).

## Sensors over MQTT

*Settings → MQTT*: your broker (`mqtt://192.168.1.10:1883`, user, password). Home Assistant creates the devices by
itself (MQTT discovery), no YAML:

- **hammond <vehicle>**: odometer, last fill-up (when, litres, price, cost), costs this month / last 30 days / this
  year, consumption, trips today and this month, and the car photo as `image.hammond_<vehicle>_photo`.
- **hammond <station> <grade>** per watched station: price, your limit, *below limit*, last check.

Sent after every change and every 5 minutes. An example dashboard with built-in cards:
[`integrations/home-assistant/dashboard.yaml`](../integrations/home-assistant/dashboard.yaml) (replace `a_class`
with your vehicle's nickname). Long-term graphs: [Grafana](../integrations/grafana/README.md).
