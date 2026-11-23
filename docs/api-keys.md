# API keys

For Home Assistant, Node-RED, scripts: *Settings → API keys → Create*. The key is **shown once**; copy it. It works
only for `/api/v1`, never for the rest of the app, and only for your own vehicles. Revoke it any time.

Send it as `Authorization: Bearer hmd_…` (or `X-Api-Key: hmd_…`):

```bash
KEY=hmd_...
curl -H "Authorization: Bearer $KEY" https://hammond.example.com/api/v1/vehicles
```

| | |
|---|---|
| `GET /api/v1/me` | currency, units |
| `GET /api/v1/vehicles` | your vehicles (ID, tank size) |
| `GET /api/v1/vehicles/{id}/stats` · `/fillups` · `/expenses` · `/mileage?since=…&until=…` | the vehicle's data |
| `GET /api/v1/vehicles/{id}/trips?from=2026-11-01&to=2026-11-30` · `/trips.csv` | the trip log |
| `GET /api/v1/me/priceWatches` · `/{id}/history?days=30` or `?from=…&to=…` | watched stations and their prices |
| `GET /api/v1/summary` | everything Home Assistant gets over MQTT, as JSON |
| `POST /api/v1/vehicles/{id}/detections` | a detected fill-up (fuel levels or litres) → see [Home Assistant](home-assistant.md) |
| `POST /api/v1/command` `{"text": "prices"}` | the chat commands, for your own bot |

Keep keys out of dashboards and logs: in Home Assistant put them in `secrets.yaml`.
