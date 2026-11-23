[![Contributors][contributors-shield]][contributors-url] [![Forks][forks-shield]][forks-url] [![Stargazers][stars-shield]][stars-url] [![Issues][issues-shield]][issues-url] [![AGPL-3.0 License][license-shield]][license-url]

> **Maintained fork** of [akhilrex/hammond](https://github.com/akhilrex/hammond) (unmaintained since 2023).
> Same app and data format; dependencies are kept patched automatically (Renovate → end-to-end test → patch release).
> Image: `ghcr.io/nenchi/hammond`. Changes from upstream 1.0.0:
>
> - Go 1.16 → 1.27, all Go modules updated; `dgrijalva/jwt-go` → `golang-jwt/jwt/v5`, `satori/go.uuid` → `google/uuid`,
>   archived `gin-gonic/contrib` → `gin-contrib/static`, `gocron` → stdlib ticker
> - UI built on Node 24 with vue-cli 5 (Vue 2.7, axios 1.x); dev-only tooling removed
> - **Security:** `POST /api/auth/initialize` no longer creates an admin after setup (upstream fell through the check)
> - **Security:** an unset `JWT_SECRET` no longer means an empty signing key: a random one is generated in `/config/jwt_secret`;
>   tokens must be HS256. After upgrading, everyone logs in once more.
> - `Authorization: Bearer <token>` works (the prefix check was off by one)
> - Base images pinned by digest, end-to-end smoke test in CI (`tests/smoke_test.py`)


## Automatic fill-up detection (Home Assistant / API)

hammond can take **detected fill-ups** from Home Assistant, Node-RED, an OBD script or anything else that can
send HTTP. A detection becomes a **pending fill-up** (menu item *Detected*); you complete it with the price, and
only then does it count. Nothing in hammond is car-specific: anything with a fuel/battery level in % and an
odometer works.

**1. In hammond**

- *Settings → API keys*: create a key (shown once). It works only for `/api/v1`, never for the rest of the app.
- Vehicle → *Edit*: set the **tank size** (litres; kWh for electric). hammond multiplies the level difference
  with it. After a few confirmed detections it suggests a size learned from what you actually filled.
- *Settings → When a detected fill-up matches one I already entered*: **Ask me** (both side by side: keep mine /
  use the detected values), **My entry wins** (detection ignored), or **Use the detected odometer and quantity**
  (written into your entry, your price stays). Entering a fill-up yourself always closes a matching pending detection.

**2. Home Assistant**

Two helpers that remember the last valid reading (cars report rarely and sensors go `unavailable`), in
`configuration.yaml` or *Settings → Helpers*:

```yaml
input_number:
  car_last_fuel_level: { min: 0, max: 100, step: 0.1, mode: box }
  car_last_fuel_odometer: { min: 0, max: 9999999, step: 1, mode: box }

rest_command:
  hammond_detection:
    url: "https://hammond.example.com/api/v1/vehicles/{{ vehicle_id }}/detections"
    method: post
    headers:
      Authorization: !secret hammond_api_key   # secrets.yaml: hammond_api_key: "Bearer hmd_..."
    content_type: application/json
    payload: "{{ payload | tojson }}"
```

Then import the blueprint
[`integrations/home-assistant/hammond_fillup_detection.yaml`](integrations/home-assistant/hammond_fillup_detection.yaml)
(*Settings → Automations → Blueprints → Import*, paste the raw GitHub URL) and create an automation from it: pick
the level and odometer sensors, the two helpers, the hammond vehicle ID and your phone's notify service. When the
level jumps by at least 10 % it waits until the value is stable, sends the detection and notifies you with a link
to complete it. If the last reading before the fill-up was taken many km earlier, hammond marks the estimate
*check quantity*.

**Dashboards:** an example Home Assistant dashboard for hammond's MQTT sensors
([`integrations/home-assistant/dashboard.yaml`](integrations/home-assistant/dashboard.yaml)) and a Grafana dashboard
for longer statistics ([`integrations/grafana`](integrations/grafana/README.md)).

**Station prices (optional):** with *Admin → Site settings → Fuel prices* set up (Germany: free key from
[Tankerkönig](https://creativecommons.tankerkoenig.de); Austria: E-Control, Slovenia: goriva.si, Croatia: MZOE,
France: prix-carburants, Portugal: DGEG, Italy: MIMIT, Spain: Ministerio (fetched with curl), no key;
other countries (SK, CZ, PL, HU, CH, NL, BE…): [Fuelo](https://developers.fuelo.net) with your own key: the free plan
(100 requests a day, 5 a minute) lists stations with the country's average price, Pro adds per-station prices (Price
watch works then); hammond checks the plan once a day, counts requests and stops at 90 a day and at the minute limit;
or any Tankerkönig-compatible URL; hammond looks up the country of the position via OpenStreetMap Nominatim and
asks only that country's profiles, then custom ones, and says when a country has no profile), the fill-up form has a
**Stations nearby** button (uses the phone's location) and, if the detection sent a position (blueprint input
*Location*: your phone's `device_tracker`, or the car's), lists the stations near it. Picking one fills in station,
price and grade (per vehicle: E5 / E10 / Diesel / 98-100 / Premium diesel / LPG; the last three only where the
country publishes them, not DE/AT); the arrow next to the address opens navigation to the station
(on Android any navigation app). Instead of the location: **Map** (OpenStreetMap, tap a point; full screen on a
phone), a place/address search, a radius of 2–25 km and the price source (Auto = the country of the point).
Prices are the current ones: a suggestion, check the receipt.
Prices: MTS-K via Tankerkönig (CC BY 4.0).

**Price watch:** *Price watch* in the menu: favourite stations (★ Watch in the list or on the map), checked in the
background (interval per price source: Germany/Austria hourly, Slovenia/Croatia/Italy daily, France/Portugal 6 h; 15 min
to 7 days), every price change kept. A price limit per station sends one [Gotify](https://gotify.net) message when the
price drops to it (again only after it went above). Graph with zoom/drag/period selection, your fill-ups at that station,
and the average price per hour of the day (the cheapest hours).

**3. Any other system**

```bash
# vehicles visible to the key (ID, tank size)
curl -H "Authorization: Bearer hmd_..." https://hammond.example.com/api/v1/vehicles

# a detection: levels in % (estimated with the tank size) …
curl -X POST -H "Authorization: Bearer hmd_..." -H "Content-Type: application/json" \
  -d '{"odoReading": 81068, "odoAtBefore": 81066, "levelBefore": 74, "levelAfter": 99, "source": "OBD"}' \
  https://hammond.example.com/api/v1/vehicles/<vehicle-id>/detections
# … or the quantity directly: {"odoReading": 81068, "fuelQuantity": 11.2}
```

Fields: `odoReading` (required), `fuelQuantity` *or* `levelBefore` + `levelAfter`, optional `odoAtBefore`, `lat` + `lng`,
`isTankFull` (default: level after ≥ 97 %), `date` (ISO 8601, default now), `source`. The response has `result`
(`pending`, `duplicate`, `ignored`, `applied`), a ready-to-use notification `message` and the `path` to open.


## Trip log (Home Assistant)

Vehicle → **Trips**: every drive (engine on → off) is read from your Home Assistant history every 10 minutes
(**Sync now** for at once; the first sync reads the last 10 days). Drives with a short stop between them
(15 minutes by default, e.g. a fuel stop) are one trip; join or split them by hand.

- **Setup** on the Trips page: the ignition entity (required), and optionally the fuel level and the trip computer's
  distance and consumption since start. With a Mercedes (mbapi2020) **Suggest from odometer entity** fills them in.
  Trip computers that keep counting across short stops are handled (each drive = end − start value).
- **Fuel cost, first in, first out:** a trip burns the oldest fuel in the tank at the price it was bought for, then
  the next fill-up's price. A full-tank fill-up resets the tank content.
- Per trip: business/private, purpose, notes, and costs (toll, vignette, parking… saved as expenses of the trip).
- **CSV** export and **Print / PDF** (the browser's "Save as PDF") for a period.
- Admin → Site Settings: **lock** trip times and kilometres so only admins can change them; users still add purpose,
  notes and costs.
- API: `GET /api/v1/vehicles/{id}/trips?from=2026-11-01&to=2026-11-22` (and `trips.csv`) with an API key.

## Home Assistant dashboard (MQTT)

Settings → **MQTT**: your broker (`mqtt://192.168.1.10:1883`, user, password). hammond announces its sensors with
Home Assistant's MQTT discovery, so HA creates the devices by itself, no YAML:

- **hammond <vehicle>**: odometer, last fill-up (date, litres, price, cost), fuel / other / total costs this month and
  the last 30 days, fill-ups, litres and average price of the last 30 days, costs this year, consumption (90 days),
  trips today and this month (distance, business distance, driving time, cost). The "this month / today / this year"
  totals tell HA when they start over, so HA's statistics cards show correct bars per month.
- **hammond <station> <grade>** per watched station: price, price limit, below limit (binary sensor), last check

Values are sent after every change (fill-up, expense, trip, price) and every 5 minutes; turning MQTT off shows the
devices as unavailable. Build your own cards from the sensors (history graphs, statistics, automations such as
"E10 below 1.70 → notify"). The same numbers as JSON: `GET /api/v1/summary` with an API key.

## Notifications and the Telegram bot

Settings → **Notifications**: a list of channels, each on/off, editable, deletable, with **Test**. A price alert goes
to every channel that is on; tapping it opens Google Maps navigation to the station.

- **Gotify**: server URL + app token
- **ntfy**: topic URL (`https://ntfy.sh/my-hammond-alerts`), optional access token for protected topics
- **Matrix**: homeserver URL, room ID or alias, access token of the account that posts
- **Telegram**: your own bot (@BotFather → `/newbot` → token). Link your chat by sending `/start <code>` (the code is
  shown in the settings); only that chat is answered. hammond asks Telegram for new messages (no webhook, no open port);
  without a Telegram channel nothing connects to Telegram.
- **Matrix as a bot, like Telegram**: a bot account (e.g. `@hammond`; Continuwuity/Conduit admin room:
  `!admin users create-user hammond`) and its access token (the form's *Get the token* logs in once with the bot's
  username and password and fills it in; the password isn't saved), an unencrypted room
  with the bot invited, *Answer the fuel commands in this room* on. hammond joins and reads that room (sync, outgoing
  only), you link yourself with `/start <code>`; then the same commands, charts, PDF/CSV files, "fill-up detected →
  answer with the price" and receipt pictures (→ Quick Entry) as on Telegram. Other people in the room are ignored.

### Chat commands (Telegram and Matrix)

With *Answer the fuel commands* on, the chat works like a small app. `[car]` is a car's number (`cars`), name or plate;
with one car you never name it, with several the reading commands show all and the writing ones ask (or use the
default set with `car 2`).

| Read | Write |
|---|---|
| `prices`, `watched`, `price 3 30`, `cheap-time e10` | `fill 40.5 1.759 [km] [partial] [car]` (km from Home Assistant when left out, station as last time) |
| `stats [car]`, `graph fuel 90 [car]` (a chart picture) | `cost toll 12.50 A3` (on today's trip, else an expense) |
| `trips 10 [car]`, `trips 30 pdf` (a PDF or CSV file) | `purpose Customer X`, `business`, `private` (the latest trip) |
| `cars`, `help` | `undo` (the last fill-up or cost, 10 minutes) |

- **Fill-up detected** (e.g. by Home Assistant): the chat gets "38 l at 81300, answer with the price"; answering
  `1.759` saves it.
- **A photo** of a receipt or the pump's display becomes a Quick Entry, to complete later.
- Writing commands go through the same checks as the web app.

The same commands for other chat bots: `POST /api/v1/command {"text": "prices"}` with an API key (text, plus a PNG or a
file base64-encoded).

<!-- PROJECT LOGO -->
<br />
<p align="center">
  <!-- <a href="https://github.com/akhilrex/hammond">
    <img src="images/logo.png" alt="Logo" width="80" height="80">
  </a> -->

  <h1 align="center" style="margin-bottom:0">Hammond</h1>
  <p align="center">Current Version - see <a href="VERSION">VERSION</a></p>

  <p align="center">
    A self-hosted vehicle expense tracking system with support for multiple users.
    <br />
    <a href="https://github.com/nenchi/hammond"><strong>Explore the docs »</strong></a>
    <br />
    <br />
    <!-- <a href="https://github.com/akhilrex/hammond">View Demo</a>
    · -->
    <a href="https://github.com/nenchi/hammond/issues">Report Bug</a>
    ·
    <a href="https://github.com/nenchi/hammond/issues">Request Feature</a>
        ·
    <a href="Screenshots.md">Screenshots</a>
  </p>
</p>

<!-- TABLE OF CONTENTS -->

## Table of Contents

- [About the Project](#about-the-project)
  - [Motivation](#motivation)
  - [Built With](#built-with)
  - [Features](#features)
- [Installation](#installation)
- [Contributing](#contributing)
- [License](#license)
- [Roadmap](#roadmap)
- [Contact](#contact)

<!-- ABOUT THE PROJECT -->

## About The Project

Hammond is a self hosted vehicle management system to track fuel and other expenses related to all of your vehicles. It supports multiple users sharing multiple vehicles. It is the logical successor to Clarkson which has not been updated for quite some time now.

_Developers Note: This project is under active development which means I release new updates very frequently. It is recommended that you use something like [watchtower](https://github.com/containrrr/watchtower) which will automatically update your containers whenever I release a new version or periodically rebuild the container with the latest image manually._

__Also check out my other self-hosted, open-source solution - [Podgrab](https://github.com/akhilrex/podgrab) - Podcast download and archive manager and player.__
### Motivation and Developer Notes

I was looking for a fuel tracking system and stumbled upon Clarkson. Although it did most of what I needed it has not been updated for quite a lot of time. Since I had some bandwidth available as my previous open source project [Podgrab](http://github.com/akhilrex/podgrab) had become quite stable now, my first thought was to contribute to the Clarkson project only. I soon realized that the architecture that Clarkson had used was not really be that extensible now and would warrant a complete rewrite only. So I decided to build Hammond - The successor to Clarkson.

The current version of Hammond is written using GO for backend and Vuejs for the front end. Originally I had thought of using the same tech stack for both frontend and the backend so that it became easier for users and other developers to use, deploy and contribute. Which is why the first version of Hammond has a NestJS backend complete with all the bells and whistles (GraphQL, Prisma and what nots). But I eventually decided to rebuild the backend in GO just to keep the container size small. No matter how much you can optimize the sheer size of the node_modules will always add bulk to your containers. I host all my tools on my Raspberry Pi. It only makes sense to keep the container size as small as possible.

Also I had initially thought of a 2 container approach (1 for backend and 1 for the frontend) so that they can be independently maintained and updated. I eventually decided against this idea for the sake of simplicity. Although it is safe to assume that most self-hosters are fairly tech capable it still is much better to have a single container that you can fire and forget.

![Product Name Screen Shot][product-screenshot] [More Screenshots](Screenshots.md)

### Built With

- [Go](https://golang.org/)
- [Go-Gin](https://github.com/gin-gonic/gin)
- [GORM](https://github.com/go-gorm/gorm)
- [SQLite](https://www.sqlite.org/index.html)
- [VueJS](https://vuejs.org/)
- [Vuex](https://vuex.vuejs.org/)
- [Buefy](https://buefy.org/)

### Features

- Add/Manage multiple vehicles and users, share vehicles across users
- Track fuel and other expenses, attachments per vehicle, Quick Entries (photo of a receipt, complete later)
- Vehicle level and overall reporting, charts with zoom; import from Fuelly and Clarkson; CSV export and backup
- Light/dark theme, works on phones

Added in this fork:

- **Trip log** from Home Assistant: drives joined across short stops, fuel cost first in, first out, purpose/notes,
  tolls and other costs per trip, CSV and print/PDF, admin lock for times and kilometres ([Trip log](#trip-log-home-assistant))
- **Detected fill-ups** from Home Assistant or any script, with duplicate handling and a fuel gauge calibration
  ([Automatic fill-up detection](#automatic-fill-up-detection-home-assistant--api))
- **Odometer from Home Assistant** in the fill-up form; warnings for odometer typos and quantities above the tank size
- **Station prices** near you or near a detection: Germany, Austria, Slovenia, Croatia, France, Portugal, Italy, Spain
  (free sources need no key), own price profiles per user
- **Price watch:** favourite stations and grades, price history, cheapest hours, limits with an alert (Gotify, ntfy,
  Matrix or Telegram) that opens Google Maps navigation
- **Telegram bot** per user: fuel prices, charts, statistics and the trip log (PDF/CSV) in the chat
- **Home Assistant dashboard over MQTT:** devices per vehicle and watched station created by MQTT discovery
- **EV charging:** charging places with tariffs (fixed, time-based, Home Assistant price sensors), EVCC sessions as
  fill-ups
- **API keys** for `/api/v1`: detections (write) and read-only data for scripts and chat bots (vehicles, fill-ups,
  expenses, mileage, price watches, trips, summary) and the chat commands

## Installation

The easiest way to run Hammond is to run it as a docker container.

### Using Docker

Simple setup without mounted volumes (for testing and evaluation)

```sh
  docker run -d -p 3000:3000 --name=hammond ghcr.io/nenchi/hammond:latest
```

Binding local volumes to the container

```sh
   docker run -d -p 3000:3000 --name=hammond -v "/host/path/to/assets:/assets" -v "/host/path/to/config:/config" ghcr.io/nenchi/hammond:latest
```

### Using Docker-Compose

Modify the docker compose file provided [here](https://github.com/nenchi/hammond/blob/main/docker-compose.yml) to update the volume and port binding and run the following command

```yaml
version: '2.1'
services:
  hammond:
    image: ghcr.io/nenchi/hammond:latest
    container_name: hammond
    volumes:
      - /path/to/config:/config
      - /path/to/data:/assets
    ports:
      - 3000:3000
    restart: unless-stopped
```

```sh
   docker-compose up -d
```

### Build from Source / Ubuntu Installation

Although personally I feel that using the docker container is the best way of using and enjoying something like hammond, a lot of people in the community are still not comfortable with using Docker and wanted to host it natively on their Linux servers. Follow the link below to get a guide on how to build hammond from source.

[Build from source / Ubuntu Guide](docs/ubuntu-install.md)

### Environment Variables

| Name | Description                                                                                                                | Default |
| ---- | -------------------------------------------------------------------------------------------------------------------------- | ------- |
| JWT_SECRET | The secret used to sign the JWT token. Optional. If unset, a random secret is generated once and stored in `/config/jwt_secret`| A super strong secret that needs to be changed | 
| PORT | Change the internal port of the application. If you change this you might have to change your docker configuration as well | (empty) |

### Setup

When you open Hammond for the first time after a fresh install, you will be presented with the option to either import data from an existing Clarkson instance or setup a fresh instance.

#### Migration from Clarkson

You will have to ensure that the Clarkson database is accessible from the Hammond deployment. In case it is not directly possible, you can always take a backup of the Clarkson database and host it somewhere accessible to Hammond using a temporary container. If the access problem is sorted, you will have to enter the connection string the Clarkson database in the following format.

```
        user:pass@tcp(127.0.0.1:3306)/dbname?charset=utf8mb4&parseTime=True&loc=Local
```

You can check the connectivity from the screen as well.

Note: All the users migrated from Clarkson will have their passwords changed to `hammond`

#### Fresh setup

You will have to provide your name, email and password so that an admin user can be created for you.

Once done you will be taken to the login page.

Go through the settings page once and change relevant settings before you start adding vehicles and expenses.

## Contributing

### Dev Setup

If you want to contribute to the project you need to set it up
for development first.

Fork and clone the project. Once you have it on your own machine,
open up a terminal and navigate to the `server/` directory.

In the `server/` directory run the command `go run main.go`.
After some initial
setup, the server should be listening on at port `3000`.

Next, open a new terminal. Navigate to the `ui/` directory and run `npm install`.
This will install all the dependencies for the frontend.
After the command is done running, run `npm run dev`. After some output, the
frontend should be accessible at `http://localhost:8080`.

If you are sent straight to the login screen, try closing the page and opening
it again. You should be greeted with a setup wizard the first time you run the
project.

Now, simply follow the instructions in order to set up your fresh install.

## License

Distributed under the GPL-3.0 License. See `LICENSE` for more information.

## Roadmap

- [ ] Trip start/stop and addresses from the phone's live location
- [ ] Vignettes per country (one vignette counts for every trip in its period)
- [ ] More reports
- [ ] Vehicle specific reminders (servicing etc)
- [ ] Native installer for Windows/Linux/MacOS

<!-- CONTACT -->

## Contact

This fork: [github.com/nenchi/hammond](https://github.com/nenchi/hammond), issues and pull requests welcome.

If the fork is useful to you: <a href="https://paypal.me/nenadd26" target="_blank">buy me a coffee via PayPal</a> ☕

### Original author

hammond was created by Akhil Gupta - [@akhilrex](https://twitter.com/akhilrex) · [LinkedIn](https://linkedin.com/in/akhilrex) -
[github.com/akhilrex/hammond](https://github.com/akhilrex/hammond)

<a href="https://www.buymeacoffee.com/akhilrex" target="_blank"><img src="https://cdn.buymeacoffee.com/buttons/v2/default-yellow.png" alt="Buy Me A Coffee" style="width: 217px !important;height: 60px !important;" ></a>

<!-- MARKDOWN LINKS & IMAGES -->
<!-- https://www.markdownguide.org/basic-syntax/#reference-style-links -->

[contributors-shield]: https://img.shields.io/github/contributors/nenchi/hammond.svg?style=flat-square
[contributors-url]: https://github.com/nenchi/hammond/graphs/contributors
[forks-shield]: https://img.shields.io/github/forks/nenchi/hammond.svg?style=flat-square
[forks-url]: https://github.com/nenchi/hammond/network/members
[stars-shield]: https://img.shields.io/github/stars/nenchi/hammond.svg?style=flat-square
[stars-url]: https://github.com/nenchi/hammond/stargazers
[issues-shield]: https://img.shields.io/github/issues/nenchi/hammond.svg?style=flat-square
[issues-url]: https://github.com/nenchi/hammond/issues
[license-shield]: https://img.shields.io/github/license/nenchi/hammond.svg?style=flat-square
[license-url]: https://github.com/nenchi/hammond/blob/main/LICENSE
[linkedin-shield]: https://img.shields.io/badge/-LinkedIn-black.svg?style=flat-square&logo=linkedin&colorB=555
[linkedin-url]: https://linkedin.com/in/akhilrex
[product-screenshot]: images/screenshot.jpg
