# hammond in Grafana

[`hammond-car.json`](hammond-car.json): costs, consumption, watched station prices, kilometres per day and,
optionally, the car's own sensors (fuel level, range, trip computer, tyres) over any period Grafana's time
picker allows.

## How the data gets there

hammond → MQTT → Home Assistant → Home Assistant's
[InfluxDB integration](https://www.home-assistant.io/integrations/influxdb/) → VictoriaMetrics (it accepts
InfluxDB's line protocol) → Grafana (Prometheus data source pointing at VictoriaMetrics).

The queries expect Home Assistant's InfluxDB integration with `measurement_attr: entity_id`, so the metric of
an entity is `<entity id>_value`, e.g. `sensor.hammond_a_class_costs_this_month_value`:

```yaml
# configuration.yaml
influxdb:
  host: victoriametrics      # your VictoriaMetrics, port 8428
  port: 8428
  measurement_attr: entity_id
  include:
    domains: [sensor, binary_sensor]
```

History starts when Home Assistant starts sending; older fill-ups and prices are in hammond itself
(Stats and Price watch have a custom date range).

## Import

Grafana → Dashboards → New → Import → upload `hammond-car.json` → pick the VictoriaMetrics/Prometheus data
source. At the top:

- **Car (hammond)**: your vehicles, found from their sensors.
- **Car integration prefix** (optional): the entity prefix of the car's own sensors, e.g. `of_n1372` for the
  Mercedes me integration's `sensor.of_n1372_fuel_level`. Other car integrations name their sensors differently:
  edit those panels' queries.
