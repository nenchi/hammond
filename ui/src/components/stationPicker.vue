<script>
import axios from 'axios'
import { mapState } from 'vuex'

import { GRADE_LABELS as GRADES } from '@utils/fuel-grades'
import L from 'leaflet'
import 'leaflet/dist/leaflet.css'
import { addFullscreenButton } from '@utils/map-fullscreen'

// station names come from the price services: never as HTML
const escapeHtml = (t) => String(t || '').replace(/[&<>"']/g, (c) => `&#${c.charCodeAt(0)};`)

// Stations nearby with current prices (Admin → Site Settings → Fuel prices, a list of profiles). Picking one emits
// { name, price, grade }. The price is today's, so it's a suggestion the user can still change.
export default {
  props: {
    vehicle: { type: Object, required: true },
    // a detected fill-up with lat/lng: list the stations near it right away
    detection: { type: Object, default: null },
  },
  data() {
    return { loading: false, error: '', result: null, heading: '', pos: null, profiles: [], loaded: false, placeText: '', places: [], showMap: false, rad: null, source: '' }
  },
  computed: {
    ...mapState('utils', ['settings']),
    ...mapState('users', ['me']),
    // Settings → search radius (1–25 km)
    // Settings → search radius (1–25 km), or what was picked here
    radius() {
      return this.rad || (this.me && this.me.stationRadiusKm) || 2
    },
    radiusChoices() {
      return [...new Set([2, 5, 10, 15, 20, 25, this.radius])].sort((a, b) => a - b)
    },
    mode() {
      return (this.me && this.me.stationSuggestions) || 'location'
    },
    enabled() {
      return this.profiles.length > 0 && this.mode !== 'off'
    },
    // where to switch it on instead of nothing (own profiles; admins also share site ones)
    showSetupHint() {
      return this.loaded && !this.enabled && this.mode !== 'off'
    },
    // the source's currency when it says one (Fuelo: CZK, PLN…), else the user's
    currency() {
      return (this.result && this.result.currency) || (this.me && this.me.currency) || ''
    },
    // no station prices (Fuelo's free plan): "Slovakia average: 95 1.620 EUR · Diesel 1.580 EUR"
    averageText() {
      const r = this.result
      if (!r || !r.averages || !Object.keys(r.averages).length) return ''
      let country = (r.averageCountry || '').toUpperCase()
      try {
        country = new Intl.DisplayNames(['en'], { type: 'region' }).of(country)
      } catch (e) {}
      const parts = r.grades.filter((g) => r.averages[g]).map((g) => `${this.label(g)} ${r.averages[g].toFixed(3)} ${this.currency}`)
      return `${country} average today: ${parts.join(' · ')}`
    },
    gradeLabel() {
      return this.result ? this.result.grades.map((g) => GRADES[g] || g).join(' / ') : ''
    },
    // cheapest price among all stations and grades: highlighted
    cheapest() {
      let min = null
      for (const st of (this.result && this.result.stations) || []) for (const p of Object.values(st.prices)) min = min === null || p < min ? p : min
      return min
    },
  },
  watch: {
    detection() {
      this.nearDetection()
    },
  },
  mounted() {
    // own profiles first, then the shared ones: the order the server asks them in
    axios
      .get('/api/fuelPrices/available')
      .then(({ data }) => (this.profiles = data))
      .catch(() => {})
      .finally(() => {
        this.loaded = true
        this.nearDetection()
      })
  },
  beforeDestroy() {
    if (this.map) this.map.remove()
  },
  methods: {
    // a detected fill-up with a position: its stations right away (once the profiles are known)
    nearDetection() {
      const d = this.detection
      if (this.loaded && d && d.lat !== null && d.lng !== null && d.lat !== undefined && this.mode === 'location') {
        this.load(d.lat, d.lng, 'Stations near the detected location')
      }
    },
    nearMe() {
      if (!navigator.geolocation) {
        this.error = 'This browser has no location access.'
        return
      }
      this.loading = true
      this.error = ''
      navigator.geolocation.getCurrentPosition(
        (pos) => this.load(pos.coords.latitude, pos.coords.longitude, 'Stations nearby'),
        (err) => {
          this.loading = false
          this.error = err.code === 1 ? 'Location permission denied.' : 'Location not available: ' + err.message
        },
        { enableHighAccuracy: true, timeout: 15000, maximumAge: 60000 }
      )
    },
    // a place or address instead of the device's location (desktop browsers often have none);
    // one match lists its stations, several are offered to pick from
    searchPlace() {
      if (!this.placeText.trim()) return
      this.loading = true
      this.error = ''
      this.places = []
      axios
        .get('/api/places', { params: { q: this.placeText } })
        .then(({ data }) => (data.length === 1 ? this.pickPlace(data[0]) : (this.places = data)))
        .catch((ex) => (this.error = (ex.response && ex.response.data && ex.response.data.error) || ex.message))
        .finally(() => (this.loading = false))
    },
    pickPlace(p) {
      this.places = []
      this.load(p.lat, p.lng, 'Stations near ' + p.name.split(',').slice(0, 2).join(','))
    },
    sourceChanged() {
      this.$nextTick(() => this.pos && this.load(this.pos.lat, this.pos.lng, this.pos.heading))
    },
    setRadius(r) {
      this.rad = r
      if (this.pos) this.load(this.pos.lat, this.pos.lng, this.pos.heading)
    },
    // a station's popup on the map: name, address, price, and the same actions as in the list
    popupFor(st, price) {
      const div = document.createElement('div')
      div.innerHTML = `<strong>${escapeHtml(st.brand || st.name)}</strong><br>${escapeHtml(st.address)}<br>${price}<br>`
      const button = (text, cls, fn) => {
        const b = document.createElement('button')
        b.type = 'button'
        b.className = `button is-small mt-2 mr-1 ${cls}`
        b.textContent = text
        b.onclick = () => {
          this.map.closePopup()
          fn()
        }
        div.appendChild(b)
      }
      // fill-up form: take this station (and leave full screen to see the form)
      if (this.$listeners.pick && st.grade)
        button('Use for fill-up', 'is-primary popup-use', () => {
          this.exitFullscreen()
          this.pick(st)
        })
      if (st.grade) button('★ Watch', 'popup-watch', () => this.watch(st))
      return div
    },
    // OpenStreetMap: tap a point to list its stations; the stations found are shown as price markers.
    // ponytail: circle markers, not Leaflet's image pins (they break with webpack).
    toggleMap() {
      this.showMap = !this.showMap
      if (!this.showMap) {
        if (this.map) this.map.remove()
        this.map = null
        return
      }
      this.$nextTick(() => {
        const start = this.pos ? [this.pos.lat, this.pos.lng] : [47.5, 12.5] // Alps: between DE, AT, SI, HR
        this.map = L.map(this.$refs.map).setView(start, this.pos ? 13 : 6)
        L.tileLayer('https://tile.openstreetmap.org/{z}/{x}/{y}.png', {
          maxZoom: 19,
          attribution: '&copy; <a href="https://www.openstreetmap.org/copyright">OpenStreetMap</a>',
        }).addTo(this.map)
        this.layer = L.layerGroup().addTo(this.map)
        this.exitFullscreen = addFullscreenButton(this.map)
        this.map.on('click', (e) => this.load(e.latlng.lat, e.latlng.lng, 'Stations near the point on the map'))
        this.drawStations()
      })
    },
    drawStations() {
      if (!this.map || !this.pos) return
      this.layer.clearLayers()
      const circle = L.circle([this.pos.lat, this.pos.lng], { radius: this.radius * 1000, weight: 1, fillOpacity: 0.05 }).addTo(this.layer)
      for (const st of (this.result && this.result.stations) || []) {
        if (!st.lat && !st.lng) continue
        const price = st.price ? st.price.toFixed(3) : '–'
        // only the price as label (names overlapped with many stations); tapping the dot shows the name
        L.circleMarker([st.lat, st.lng], {
          radius: 8,
          color: '#fff',
          weight: 2,
          fillColor: st.price === this.cheapest ? '#39a275' : '#3273dc',
          fillOpacity: 1,
          bubblingMouseEvents: false, // a tap on a station isn't a new search point
        })
          .bindTooltip(price, { permanent: true, direction: 'top', offset: [0, -8], interactive: true }) // the label opens it too
          .bindPopup(() => this.popupFor(st, price))
          .addTo(this.layer)
      }
      this.map.fitBounds(circle.getBounds()) // the whole search circle, 2 km or 25 km
    },
    // profile empty = the server asks the profiles in order, the first with stations nearby answers
    // source '' = the server picks the profile of the point's country
    load(lat, lng, heading, profile = this.source) {
      if (!this.enabled) return
      this.loading = true
      this.error = ''
      this.pos = { lat, lng, heading }
      axios
        .get(`/api/vehicles/${this.vehicle.id}/stations`, { params: { lat, lng, rad: this.radius, profile } })
        .then(({ data }) => {
          this.result = data
          this.heading = heading
          this.drawStations()
          if (!data.stations.length)
            this.error =
              (data.country ? data.country + ': n' : 'N') +
              `o stations within ${this.radius} km (asked: ${data.profileName}).` +
              (data.errors ? ' Failed: ' + data.errors.join('; ') + '.' : '') +
              ' The radius can be changed in Settings.'
        })
        .catch((ex) => {
          this.error = (ex.response && ex.response.data && ex.response.data.error) || ex.message
        })
        .finally(() => (this.loading = false))
    },
    label(g) {
      return GRADES[g] || g
    },
    // Android: geo: opens the app chooser (Google Maps, Waze, OsmAnd…); elsewhere Google Maps directions
    navUrl(st) {
      const ll = `${st.lat},${st.lng}`
      return /Android/i.test(navigator.userAgent)
        ? `geo:${ll}?q=${ll}(${encodeURIComponent(st.brand || st.name)})`
        : `https://www.google.com/maps/dir/?api=1&destination=${ll}`
    },
    // a link that opens for anyone (geo: only works on Android)
    mapsUrl(st) {
      return `https://www.google.com/maps/search/?api=1&query=${st.lat},${st.lng}`
    },
    // the phone's share sheet (WhatsApp, Signal, SMS…); elsewhere copied to the clipboard
    share(st) {
      const prices = this.result.grades
        .filter((g) => st.prices[g] !== undefined)
        .map((g) => `${this.label(g)} ${st.prices[g].toFixed(3)} ${this.currency}`.trim())
        .join(', ')
      const title = st.brand || st.name
      const text = [title, st.address, prices].filter(Boolean).join('\n')
      const url = this.mapsUrl(st)
      if (navigator.share) {
        navigator.share({ title, text, url }).catch(() => {}) // cancelled: nothing to do
        return
      }
      const all = `${text}\n${url}`
      const done = () => this.$buefy.toast.open({ message: 'Copied: ' + title, type: 'is-success', duration: 2500 })
      if (navigator.clipboard) navigator.clipboard.writeText(all).then(done, () => window.prompt('Copy:', all))
      else window.prompt('Copy:', all)
    },
    // Price watch: this station with its preferred grade (the grade and a price limit are set there)
    watch(st) {
      axios
        .post('/api/me/priceWatches', {
          profileId: this.result.profile,
          stationId: st.id,
          name: st.brand || st.name,
          address: st.address,
          lat: st.lat,
          lng: st.lng,
          grade: st.grade,
          lastPrice: st.price,
        })
        .then(({ data }) => {
          this.$emit('watched', data)
          this.$buefy.toast.open({ message: `Watching ${data.name} (${this.label(data.grade)}): Price watch`, type: 'is-success', duration: 3000 })
        })
        .catch((ex) => {
          const msg = (ex.response && ex.response.data && ex.response.data.error) || ex.message
          this.$buefy.toast.open({ message: msg, type: 'is-danger', duration: 5000, position: 'is-bottom' })
        })
    },
    // tapping the station takes its preferred grade; tapping a price takes that grade
    pick(st, grade) {
      grade = grade || st.grade
      this.$emit('pick', {
        name: [st.brand || st.name, st.address].filter(Boolean).join(', '),
        price: grade ? st.prices[grade] : null,
        grade: grade ? this.label(grade) : '',
      })
      this.result = null
    },
  },
}
</script>

<template>
  <p v-if="showSetupHint" class="help mb-3 station-setup-hint"
    >📍 Stations nearby with current prices: add a profile in <router-link to="/settings">Settings → My fuel price profiles</router-link
    >.</p
  >
  <div v-else-if="enabled" class="station-picker mb-4">
    <!-- one row that wraps on a phone: location, map, radius, price source -->
    <div class="is-flex station-tools" style="gap: 0.5rem; flex-wrap: wrap">
      <b-button icon-left="map-marker-alt" :loading="loading" class="stations-nearby" @click="nearMe">Stations nearby</b-button>
      <b-button icon-left="map" class="show-map" :class="{ 'is-active': showMap }" @click="toggleMap">Map</b-button>
      <!-- search radius for this search (the default is in Settings); a changed radius asks again -->
      <b-select :value="radius" class="search-radius" title="Search radius" @input="setRadius">
        <option v-for="r in radiusChoices" :key="r" :value="r">{{ r }} km</option>
      </b-select>
      <b-select v-if="profiles.length > 1" v-model="source" class="price-profile-switch" title="Price source" @input="sourceChanged">
        <option value="">Auto (country of the point)</option>
        <option v-for="p in profiles" :key="p.id" :value="p.id">{{ p.name }}</option>
      </b-select>
    </div>
    <form class="mt-2 place-search" @submit.prevent="searchPlace">
      <b-field>
        <b-input v-model="placeText" placeholder="…or a place / address, e.g. Ljubljana" expanded maxlength="200" :has-counter="false"></b-input>
        <p class="control"><b-button native-type="submit" :loading="loading">Search</b-button></p>
      </b-field>
    </form>
    <div v-if="showMap" ref="map" class="station-map mt-2" style="height: 320px; border-radius: 4px; z-index: 0"></div>
    <p v-if="showMap" class="help">Tap the map to list the stations around that point ({{ radius }} km).</p>
    <div v-if="places.length" class="buttons mt-1 place-results">
      <b-button v-for="(p, i) in places" :key="i" size="is-small" @click="pickPlace(p)">{{ p.name }}</b-button>
    </div>
    <p v-if="error" class="help is-danger">{{ error }}</p>
    <div v-if="result && result.stations.length" class="box mt-2 p-3">
      <p class="heading">{{ heading }} · {{ gradeLabel }}</p>
      <div
        v-for="st in result.stations"
        :key="st.id"
        class="station py-2"
        :class="{ 'has-text-grey': !st.price }"
        @click="pick(st)"
      >
        <div class="is-flex is-justify-content-space-between is-align-items-center">
          <span>
            <strong>{{ st.brand || st.name }}</strong>
            <b-tag v-if="!st.isOpen" size="is-small">closed</b-tag><br />
            <small>{{ st.address }} · {{ st.dist.toFixed(1) }} km</small>
          </span>
          <span class="ml-2 has-text-right prices">
            <template v-if="Object.keys(st.prices).length">
              <a
                v-for="g in result.grades.filter((x) => st.prices[x] !== undefined)"
                :key="g"
                class="price is-block"
                :class="{ 'has-text-weight-bold has-text-success cheapest': st.prices[g] === cheapest }"
                :data-grade="g"
                @click.stop="pick(st, g)"
                >{{ label(g) }} {{ st.prices[g].toFixed(3) }} {{ currency }}</a
              >
            </template>
            <strong v-else>no {{ gradeLabel }}</strong>
          </span>
        </div>
        <!-- finger-sized: the small icon link was hard to hit on a phone -->
        <div v-if="st.lat || st.lng" class="buttons mt-1 mb-0 station-actions" @click.stop>
          <b-button tag="a" :href="navUrl(st)" target="_blank" rel="noopener" icon-left="directions" class="navigate">Navigate</b-button>
          <b-button icon-left="share-alt" class="share" @click="share(st)">Share</b-button>
          <b-button icon-left="star" class="watch" :disabled="!st.grade" @click="watch(st)">Watch</b-button>
        </div>
      </div>
      <p v-if="averageText" class="notification is-light py-2 px-3 mt-2 mb-2 country-average">{{ averageText }}</p>
      <p class="help">{{ result.profileName }} · {{ result.attribution }} · today's price, check it on the receipt</p>
    </div>
  </div>
</template>
