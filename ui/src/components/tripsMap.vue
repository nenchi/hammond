<script>
// The trips' routes on one map: a line per drive (positions from the car's tracker, else the phone),
// start ● and end ■ of each trip; tapping a line shows the trip. The picker shows all trips (the newest on
// top) or only one; focus = a trip id to pick (the "· map" link of a trip).
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
  data() {
    return { selected: "" }; // "" = all trips
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
      if (!this.withRoute.some((t) => t.id === this.selected))
        this.selected = ""; // the picked trip left the period
      this.draw();
    },
    selected() {
      this.draw();
    },
    focus(id) {
      if (!id || !this.withRoute.some((t) => t.id === id)) return;
      this.selected = id;
      this.$el.scrollIntoView({ behavior: "smooth", block: "start" });
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
      const all = [];
      // oldest first, so the newest trip lies on top where routes overlap; colors stay with their trip
      for (let i = this.withRoute.length - 1; i >= 0; i--) {
        const t = this.withRoute[i];
        if (this.selected && t.id !== this.selected) continue;
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
        all.push(...pts);
      }
      if (all.length)
        this.map.fitBounds(L.latLngBounds(all), {
          padding: [30, 30],
          maxZoom: 15,
        });
    },
    label(t) {
      const day = new Date(t.start).toLocaleDateString([], {
        weekday: "short",
        day: "numeric",
        month: "short",
      });
      const [when, places] = this.summary(t);
      return `${day} ${when}${places && places.includes("→") ? " · " + places : ""}`;
    },
  },
};
</script>

<template>
  <div class="box trips-map no-print">
    <b-field v-if="withRoute.length > 1">
      <b-select v-model="selected" size="is-small" expanded>
        <option value="">All trips ({{ withRoute.length }})</option>
        <option v-for="t in withRoute" :key="t.id" :value="t.id">
          {{ label(t) }}
        </option>
      </b-select>
    </b-field>
    <div ref="map" style="height: 360px"></div>
    <p v-if="!withRoute.length" class="help">
      No routes yet: set the car's or your phone's location in Setup. New drives
      get their route.
    </p>
  </div>
</template>
