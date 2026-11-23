# hammond in Home Assistant

- [`hammond_fillup_detection.yaml`](hammond_fillup_detection.yaml): blueprint, the car's fuel level jumps → a
  detected fill-up in hammond (see the main README).
- [`dashboard.yaml`](dashboard.yaml): an example dashboard (built-in cards only) for hammond's MQTT sensors:
  car photo, last fill-up, costs and costs per month, trips, watched station prices. Paste it into a new
  dashboard's raw configuration editor and replace `a_class` with your car (entities are named after the
  vehicle's nickname: `sensor.hammond_<nickname>_…`).

Needs hammond → Settings → MQTT. Statistics in Grafana: [`../grafana`](../grafana/README.md).
