<script>
// The trips' routes on one map: a line per drive (positions from the car's tracker, else the phone),
// start ● and end ■ of each trip; tapping a line shows the trip. focus = a trip id to zoom to.
import L from "leaflet";
import "leaflet/dist/leaflet.css";

const colors = [
  "#3273dc",
  "#e5533d",
  "#2a9d8f",
  "#e9a03b",
  "#8e5bd1",
  "#d6336c",
];

export default {
  props: {
    trips: { type: Array, required: true },
    focus: { type: String, default: "" },
    summary: { type: Function, required: true }, // trip → popup text (lines)
  },
  computed: {
    withRoute() {
      return this.trips.filter((t) =>
        t.legs.some((l) => l.route && l.route.length > 1)
      );
    },
  },
  watch: {
    trips() {
      this.draw();
    },
    focus(id) {
      const lay = this.byTrip[id];
      if (lay) {
        this.map.fitBounds(lay.getBounds(), { padding: [30, 30] });
        this.$el.scrollIntoView({ behavior: "smooth", block: "start" });
      }
    },
  },
  mounted() {
    this.map = L.map(this.$refs.map).setView([50.1, 8.7], 6);
    L.tileLayer("https://tile.openstreetmap.org/{z}/{x}/{y}.png", {
      maxZoom: 19,
      attribution:
        '&copy; <a href="https://www.openstreetmap.org/copyright">OpenStreetMap</a>',
    }).addTo(this.map);
    this.layer = L.layerGroup().addTo(this.map);
    this.draw();
  },
  beforeDestroy() {
    this.map.remove();
  },
  methods: {
    draw() {
      if (!this.map) return;
      this.layer.clearLayers();
      this.byTrip = {};
      const all = [];
      this.withRoute.forEach((t, i) => {
        const color = colors[i % colors.length];
        const group = L.featureGroup();
        const pts = t.legs.flatMap((l) => l.route || []);
        for (const l of t.legs)
          if (l.route && l.route.length > 1)
            L.polyline(l.route, { color, weight: 4, opacity: 0.8 }).addTo(
              group
            );
        L.circleMarker(pts[0], { radius: 6, color, fillOpacity: 1 }).addTo(
          group
        );
        L.circleMarker(pts[pts.length - 1], {
          radius: 6,
          color,
          fillColor: "#fff",
          fillOpacity: 1,
        }).addTo(group);
        const box = document.createElement("div");
        for (const line of this.summary(t)) {
          const p = document.createElement("div");
          p.textContent = line; // addresses come from OpenStreetMap: text, not HTML
          box.appendChild(p);
        }
        group.bindPopup(box);
        group.addTo(this.layer);
        this.byTrip[t.id] = group;
        all.push(...pts);
      });
      if (all.length)
        this.map.fitBounds(L.latLngBounds(all), {
          padding: [30, 30],
          maxZoom: 15,
        });
    },
  },
};
</script>

<template>
  <div class="box trips-map no-print">
    <div ref="map" style="height: 360px"></div>
    <p v-if="!withRoute.length" class="help">
      No routes yet: set the car's or your phone's location in Setup. New drives
      get their route.
    </p>
  </div>
</template>
