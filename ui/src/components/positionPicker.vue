<script>
import axios from 'axios'
import L from 'leaflet'
import 'leaflet/dist/leaflet.css'
import { addFullscreenButton } from '@utils/map-fullscreen'

// A position without typing coordinates: search a place/address, tap the map, or "Here" (the device's
// location). Emits 'input' with { lat, lng } (or nulls for "Remove"); radiusM draws the circle around it.
export default {
  props: {
    lat: { type: [Number, String], default: null },
    lng: { type: [Number, String], default: null },
    radiusM: { type: Number, default: 0 },
  },
  data() {
    return { text: '', places: [], error: '', searching: false }
  },
  computed: {
    has() {
      return this.lat !== null && this.lat !== '' && this.lng !== null && this.lng !== ''
    },
  },
  watch: {
    radiusM() {
      this.draw()
    },
  },
  mounted() {
    this.map = L.map(this.$refs.map).setView(this.has ? [+this.lat, +this.lng] : [47.5, 12.5], this.has ? 16 : 6)
    L.tileLayer('https://tile.openstreetmap.org/{z}/{x}/{y}.png', {
      maxZoom: 19,
      attribution: '&copy; <a href="https://www.openstreetmap.org/copyright">OpenStreetMap</a>',
    }).addTo(this.map)
    this.layer = L.layerGroup().addTo(this.map)
    addFullscreenButton(this.map)
    this.map.on('click', (e) => this.set(e.latlng.lat, e.latlng.lng))
    this.draw()
  },
  beforeDestroy() {
    this.map.remove()
  },
  methods: {
    set(lat, lng, zoom) {
      this.$emit('input', { lat: +lat.toFixed(6), lng: +lng.toFixed(6) })
      this.$nextTick(() => {
        this.draw()
        this.map.setView([lat, lng], zoom || Math.max(this.map.getZoom(), 16))
      })
    },
    clear() {
      this.$emit('input', { lat: null, lng: null })
      this.$nextTick(() => this.draw())
    },
    draw() {
      if (!this.map) return
      this.layer.clearLayers()
      if (!this.has) return
      const ll = [+this.lat, +this.lng]
      if (this.radiusM > 0) L.circle(ll, { radius: this.radiusM, weight: 1, fillOpacity: 0.1 }).addTo(this.layer)
      L.circleMarker(ll, { radius: 8, color: '#fff', weight: 2, fillColor: '#3273dc', fillOpacity: 1 }).addTo(this.layer)
    },
    search() {
      if (!this.text.trim()) return
      this.searching = true
      this.error = ''
      this.places = []
      axios
        .get('/api/places', { params: { q: this.text } })
        .then(({ data }) => (data.length === 1 ? this.pick(data[0]) : (this.places = data)))
        .catch((ex) => (this.error = (ex.response && ex.response.data && ex.response.data.error) || ex.message))
        .finally(() => (this.searching = false))
    },
    // a search result is only roughly the place: tap the map to put it exactly on the charger
    pick(p) {
      this.places = []
      this.set(p.lat, p.lng, 17)
    },
    here() {
      if (!navigator.geolocation) return (this.error = 'This browser has no location access.')
      navigator.geolocation.getCurrentPosition(
        (pos) => this.set(pos.coords.latitude, pos.coords.longitude, 17),
        (err) => (this.error = err.code === 1 ? 'Location permission denied.' : 'Location not available: ' + err.message),
        { enableHighAccuracy: true, timeout: 15000 }
      )
    },
  },
}
</script>

<template>
  <div class="position-picker">
    <!-- not a <form>: it sits inside the charging place form (Enter here must search, not save) -->
    <div class="is-flex" style="gap: 0.5rem; flex-wrap: wrap">
      <b-input
        v-model="text"
        placeholder="Search a place or address"
        style="flex: 1 1 12rem"
        maxlength="200"
        :has-counter="false"
        @keydown.native.enter.prevent="search"
      ></b-input>
      <b-button class="position-search-button" :loading="searching" @click="search">Search</b-button>
      <b-button icon-left="map-marker-alt" @click="here">Here</b-button>
    </div>
    <div v-if="places.length" class="buttons mt-1 mb-0">
      <b-button v-for="(p, i) in places" :key="i" size="is-small" @click="pick(p)">{{ p.name }}</b-button>
    </div>
    <p v-if="error" class="help is-danger">{{ error }}</p>
    <div ref="map" class="mt-2 position-map" style="height: 260px; border-radius: 4px; z-index: 0"></div>
    <p class="help"
      ><template v-if="has"
        >📍 {{ (+lat).toFixed(5) }}, {{ (+lng).toFixed(5) }} · tap the map to move it ·
        <a class="clear-position" @click="clear">remove</a></template
      ><template v-else>No position: tap the map where you charge (optional; then the place is picked automatically).</template></p
    >
  </div>
</template>
