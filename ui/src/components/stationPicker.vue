<script>
import axios from 'axios'
import { mapState } from 'vuex'
import store from '@state/store'

import { GRADE_LABELS as GRADES } from '@utils/fuel-grades'

// Stations nearby with current prices (Admin → Site Settings → Fuel prices). Picking one emits
// { name, price, grade }. The price is today's, so it's a suggestion the user can still change.
export default {
  props: {
    vehicle: { type: Object, required: true },
    // a detected fill-up with lat/lng: list the stations near it right away
    detection: { type: Object, default: null },
  },
  data() {
    return { loading: false, error: '', result: null, heading: '' }
  },
  computed: {
    ...mapState('utils', ['settings']),
    ...mapState('users', ['me']),
    mode() {
      return (this.me && this.me.stationSuggestions) || 'location'
    },
    enabled() {
      return this.settings && this.settings.fuelPriceProvider && this.mode !== 'off'
    },
    // admins see where to switch it on instead of nothing
    showSetupHint() {
      return !this.enabled && this.mode !== 'off' && store.state.auth.currentUser && store.state.auth.currentUser.role === 'ADMIN'
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
    detection: {
      immediate: true,
      handler(d) {
        if (d && d.lat !== null && d.lng !== null && d.lat !== undefined && this.mode === 'location') {
          this.load(d.lat, d.lng, 'Stations near the detected location')
        }
      },
    },
  },
  mounted() {
    if (!this.settings) store.dispatch('utils/getSettings').catch(() => {})
  },
  methods: {
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
    load(lat, lng, heading) {
      if (!this.enabled) return
      this.loading = true
      this.error = ''
      axios
        .get(`/api/vehicles/${this.vehicle.id}/stations`, { params: { lat, lng, rad: 2 } })
        .then(({ data }) => {
          this.result = data
          this.heading = heading
          if (!data.stations.length) this.error = 'No stations within 2 km.'
        })
        .catch((ex) => {
          this.error = (ex.response && ex.response.data && ex.response.data.error) || ex.message
        })
        .finally(() => (this.loading = false))
    },
    label(g) {
      return GRADES[g] || g
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
    >📍 Stations nearby with current prices: set up <router-link to="/admin/settings">Site Settings → Fuel prices</router-link
    >.</p
  >
  <div v-else-if="enabled" class="station-picker mb-4">
    <b-button icon-left="map-marker-alt" :loading="loading" class="stations-nearby" @click="nearMe">Stations nearby</b-button>
    <p v-if="error" class="help is-danger">{{ error }}</p>
    <div v-if="result && result.stations.length" class="box mt-2 p-3">
      <p class="heading">{{ heading }} · {{ gradeLabel }}</p>
      <a
        v-for="st in result.stations"
        :key="st.id"
        class="station is-flex is-justify-content-space-between is-align-items-center py-2"
        :class="{ 'has-text-grey': !st.price }"
        @click="pick(st)"
      >
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
              >{{ label(g) }} {{ st.prices[g].toFixed(3) }} {{ me ? me.currency : '' }}</a
            >
          </template>
          <strong v-else>no {{ gradeLabel }}</strong>
        </span>
      </a>
      <p class="help">{{ result.attribution }} · today's price, check it on the receipt</p>
    </div>
  </div>
</template>
