<script>
import Layout from '@layouts/main.vue'
import axios from 'axios'
import { mapState } from 'vuex'
import store from '@state/store'
import StationPicker from '@components/stationPicker.vue'
import PriceChart from '@components/priceChart.vue'
import PriceHoursChart from '@components/priceHoursChart.vue'
import { GRADE_LABELS } from '@utils/fuel-grades'
import { parseAndFormatFillupDate } from '@utils/format-date'

// Favourite stations: price checked in the background (interval per price source), history graph, and a
// Gotify message when it's at or below the limit. Stations are added with "Watch" in Stations nearby.
// "2026-11-24" in local time, for <input type="date">
const ymd = (d) => `${d.getFullYear()}-${String(d.getMonth() + 1).padStart(2, '0')}-${String(d.getDate()).padStart(2, '0')}`

export default {
  components: { Layout, StationPicker, PriceChart, PriceHoursChart },
  page: { title: 'Price watch' },
  data() {
    return {
      watches: [],
      loading: true,
      checking: false,
      days: 30, // 0 = custom range (from/to)
      customFrom: ymd(new Date(Date.now() - 90 * 864e5)),
      customTo: ymd(new Date()),
      graphs: {}, // station key → { histories: { watch id: history }, select } while its graph is shown
      settings: null,
      hours: {}, // kind → hours between checks (form)
      grades: GRADE_LABELS,
    }
  },
  computed: {
    ...mapState('vehicles', ['vehicles']),
    ...mapState('users', ['me']),
    // stations nearby needs a vehicle (its grades); electric ones have no station prices
    fuelVehicles() {
      return (this.vehicles || []).filter((v) => v.fuelType !== 4)
    },
    // stations are asked through a vehicle, but with every grade: watching isn't about one car
    vehicle() {
      return this.fuelVehicles[0]
    },
    // one card per station, its watched grades under it (each grade is its own watch with its own limit)
    stations() {
      const byKey = {}
      const out = []
      for (const w of this.watches) {
        const key = `${w.profileId}|${w.stationId}`
        if (!byKey[key]) out.push((byKey[key] = { key, name: w.name, address: w.address, kind: w.kind, watches: [] }))
        byKey[key].watches.push(w)
      }
      for (const st of out) {
        const times = st.watches.map((w) => w.checkedAt).filter(Boolean).sort()
        st.checkedAt = times[times.length - 1]
      }
      return out
    },
    // only the sources the user watches (all of them before the first watch)
    kinds() {
      if (!this.settings) return []
      const used = [...new Set(this.watches.map((w) => w.kind))]
      return used.length ? used : Object.keys(this.settings.intervals)
    },
  },
  watch: {
    days() {
      this.reloadGraphs()
    },
    customFrom() {
      if (this.days === 0) this.reloadGraphs()
    },
    customTo() {
      if (this.days === 0) this.reloadGraphs()
    },
  },
  mounted() {
    store.dispatch('vehicles/fetchVehicles').catch(() => {}) // opened directly: the store is empty
    this.load()
    this.loadSettings()
  },
  methods: {
    fmt: parseAndFormatFillupDate,
    errorText(ex) {
      return (ex.response && ex.response.data && (ex.response.data.error || JSON.stringify(ex.response.data.errors))) || ex.message
    },
    toast(message, ok = true) {
      this.$buefy.toast.open(ok ? { message, type: 'is-success', duration: 3000 } : { message, duration: 5000, position: 'is-bottom', type: 'is-danger' })
    },
    // the limit as text in the form: '' = only track
    toModel(w) {
      return { ...w, belowText: w.below ? String(w.below) : '' }
    },
    load() {
      return axios
        .get('/api/me/priceWatches')
        .then(({ data }) => (this.watches = data.map(this.toModel)))
        .finally(() => (this.loading = false))
    },
    loadSettings() {
      axios.get('/api/me/priceWatchSettings').then(({ data }) => this.applySettings(data))
    },
    applySettings(data) {
      this.settings = data
      const h = {}
      for (const k in data.intervals) h[k] = +(data.intervals[k] / 60).toFixed(2)
      this.hours = h
    },
    checkNow() {
      this.checking = true
      axios
        .post('/api/me/priceWatches/check')
        .then(({ data }) => {
          this.watches = data.map(this.toModel)
          this.reloadGraphs()
        })
        .catch((ex) => this.toast(this.errorText(ex), false))
        .finally(() => (this.checking = false))
    },
    save(w) {
      const below = w.belowText === '' ? null : parseFloat(String(w.belowText).replace(',', '.'))
      if (below !== null && !(below > 0)) return this.toast('The limit must be a price, e.g. 1.65', false)
      axios
        .put(`/api/me/priceWatches/${w.id}`, { grade: w.grade, below })
        .then(({ data }) => {
          Object.assign(w, this.toModel(data))
          this.toast('Saved')
        })
        .catch((ex) => this.toast(this.errorText(ex), false))
    },
    // one grade, or the whole station (all its grades)
    remove(st, list) {
      const what = list.length === 1 && st.watches.length > 1 ? `${st.name} · ${this.grades[list[0].grade] || list[0].grade}` : st.name
      this.$buefy.dialog.confirm({
        title: 'Stop watching',
        message: `${what}: its price history is deleted too.`,
        confirmText: 'Delete',
        type: 'is-danger',
        onConfirm: () =>
          Promise.all(list.map((w) => axios.delete(`/api/me/priceWatches/${w.id}`)))
            .catch((ex) => this.toast(this.errorText(ex), false))
            .then(() => this.load().then(this.reloadGraphs)),
      })
    },
    saveSettings() {
      const intervals = {}
      for (const k in this.hours) intervals[k] = Math.round(this.hours[k] * 60)
      axios
        .post('/api/me/priceWatchSettings', { intervals })
        .then(({ data }) => {
          this.applySettings(data)
          this.toast('Saved')
        })
        .catch((ex) => this.toast(this.errorText(ex), false))
    },
    every(minutes) {
      return minutes >= 1440 && minutes % 1440 === 0 ? `${minutes / 1440} d` : minutes >= 60 ? `${+(minutes / 60).toFixed(2)} h` : `${minutes} min`
    },
    toggle(st) {
      if (this.graphs[st.key]) return this.$delete(this.graphs, st.key)
      this.$set(this.graphs, st.key, { histories: null, select: false })
      this.fetchHistory(st)
    },
    // all grades of the station: one line each
    fetchHistory(st) {
      Promise.all(st.watches.map((w) => axios.get(`/api/me/priceWatches/${w.id}/history`, {
            params: this.days ? { days: this.days } : { from: this.customFrom, to: this.customTo },
          })))
        .then((res) => {
          const g = this.graphs[st.key]
          if (g) g.histories = Object.fromEntries(res.map(({ data }, i) => [st.watches[i].id, data]))
        })
        .catch((ex) => this.toast(this.errorText(ex), false))
    },
    reloadGraphs() {
      for (const key in this.graphs) {
        const st = this.stations.find((s) => s.key === key)
        if (st) this.fetchHistory(st)
        else this.$delete(this.graphs, key) // the station was deleted
      }
    },
    // the chart's lines: one per watched grade
    series(st) {
      const h = this.graphs[st.key].histories
      return st.watches.map((w) => ({ label: this.grades[w.grade] || w.grade, history: h[w.id], below: w.below })).filter((x) => x.history)
    },
    hasPrices(st) {
      return this.series(st).some((x) => x.history.prices.length)
    },
    resetZoom(id) {
      const c = this.$refs['chart-' + id]
      if (c && c[0]) c[0].resetZoom()
    },
  },
}
</script>

<template>
  <Layout>
    <h1 class="title">Price watch</h1>
    <p class="mb-4"
      >Favourite stations: the price is checked in the background, every change is kept for the graph, and you get an
      alert when it drops to your limit (once, again after it went above).</p
    >

    <div class="box">
      <div class="is-flex is-justify-content-space-between is-align-items-center mb-3" style="gap: 0.5rem; flex-wrap: wrap">
        <h2 class="subtitle mb-0">Watched stations</h2>
        <div class="is-flex" style="gap: 0.5rem">
          <b-select v-model.number="days" size="is-small" class="graph-days" title="Graph period">
            <option :value="7">7 days</option>
            <option :value="30">30 days</option>
            <option :value="90">90 days</option>
            <option :value="365">1 year</option>
            <option :value="0">Custom…</option>
          </b-select>
          <template v-if="days === 0">
            <input v-model="customFrom" class="input is-small graph-from" type="date" :max="customTo" title="From" style="width: auto" />
            <input v-model="customTo" class="input is-small graph-to" type="date" :min="customFrom" title="To" style="width: auto" />
          </template>
          <b-button size="is-small" icon-left="sync" :loading="checking" :disabled="!watches.length" class="check-now" @click="checkNow"
            >Check now</b-button
          >
        </div>
      </div>
      <p v-if="!loading && !watches.length" class="has-text-grey no-watches"
        >None yet: search below and press <strong>Watch</strong> on a station.</p
      >
      <div
        v-for="(st, i) in stations"
        :key="st.key"
        class="price-watch py-3"
        :style="i ? 'border-top: 1px solid rgba(128, 128, 128, 0.2)' : ''"
      >
        <div class="is-flex is-justify-content-space-between is-align-items-start" style="gap: 0.5rem">
          <div>
            <strong>{{ st.name }}</strong><br />
            <small>{{ st.address }}</small><br />
            <small class="has-text-grey"
              >{{ settings && settings.labels[st.kind] }} · every {{ settings ? every(settings.intervals[st.kind]) : '' }}
              <template v-if="st.checkedAt"> · checked {{ fmt(st.checkedAt) }}</template></small
            >
          </div>
          <div class="is-flex" style="gap: 0.5rem">
            <b-button size="is-small" :class="{ 'is-active': graphs[st.key] }" class="show-graph" @click="toggle(st)">Graph</b-button>
            <b-button size="is-small" type="is-danger" outlined icon-left="trash" class="delete-station" title="Stop watching this station" @click="remove(st, st.watches)"></b-button>
          </div>
        </div>
        <!-- each grade is its own watch: own limit, own alert (empty limit = only tracked, no message) -->
        <form
          v-for="w in st.watches"
          :key="w.id"
          class="watch-grade-row is-flex mt-2"
          :data-grade="w.grade"
          style="gap: 0.5rem; flex-wrap: wrap; align-items: flex-end"
          @submit.prevent="save(w)"
        >
          <div style="min-width: 7.5rem">
            <span class="has-text-weight-semibold">{{ grades[w.grade] || w.grade }}</span><br />
            <span class="is-size-5 has-text-weight-bold last-price" :class="{ 'has-text-success': w.alerted }"
              >{{ w.lastPrice ? w.lastPrice.toFixed(3) : '–' }}</span
            >
            <small> {{ me ? me.currency : '' }}</small>
          </div>
          <b-field label="Alert at or below" custom-class="is-small mb-1" class="mb-0">
            <b-input v-model="w.belowText" size="is-small" placeholder="empty = no alert" inputmode="decimal" class="below" style="width: 8.5rem"></b-input>
          </b-field>
          <b-button size="is-small" native-type="submit" type="is-primary" class="save-watch">Save</b-button>
          <span :title="w.below ? 'Alert at or below the limit (Settings → Notifications)' : 'Only tracked, no message'" class="alert-state">{{ w.below ? '🔔' : '🔕' }}</span>
          <b-tag v-if="w.alerted" type="is-success" size="is-small">at or below limit</b-tag>
          <b-button
            v-if="st.watches.length > 1"
            size="is-small"
            type="is-danger"
            outlined
            icon-left="trash"
            class="delete-watch"
            :title="'Stop watching ' + (grades[w.grade] || w.grade)"
            @click="remove(st, [w])"
          ></b-button>
          <p v-if="w.lastError" class="help is-danger watch-error" style="flex-basis: 100%">{{ w.lastError }}</p>
        </form>
        <div v-if="graphs[st.key] && graphs[st.key].histories" class="mt-3 price-graph">
          <p v-if="!hasPrices(st)" class="has-text-grey">No prices in this period yet.</p>
          <template v-else>
            <div class="is-flex is-align-items-center mb-1" style="gap: 0.5rem; flex-wrap: wrap">
              <b-button
                size="is-small"
                :type="graphs[st.key].select ? 'is-primary' : ''"
                class="select-period"
                @click="graphs[st.key].select = !graphs[st.key].select"
                >Select period</b-button
              >
              <b-button size="is-small" class="reset-zoom" @click="resetZoom(st.key)">Reset zoom</b-button>
              <small class="has-text-grey">{{
                graphs[st.key].select ? 'Drag over a period to zoom into it' : 'Wheel or pinch to zoom, drag to move · tap a grade to hide it'
              }}</small>
            </div>
            <PriceChart :ref="'chart-' + st.key" :series="series(st)" :select="graphs[st.key].select" style="height: 240px" />
            <template v-for="x in series(st)">
              <PriceHoursChart
                v-if="x.history.prices.length"
                :key="x.label"
                :history="x.history"
                :label="series(st).length > 1 ? x.label + ': average price by hour of day' : undefined"
                class="mt-3 price-hours"
                style="height: 160px"
              />
            </template>
            <p class="help">Cheapest hours in green: the average price per hour of the day in the shown period.</p>
          </template>
        </div>
      </div>
    </div>

    <div class="box add-stations">
      <h2 class="subtitle">Add stations</h2>
      <p v-if="!fuelVehicles.length" class="has-text-grey">Add a petrol or diesel vehicle first: stations nearby are looked up through it.</p>
      <template v-else>
        <StationPicker
          :vehicle="vehicle"
          all-grades
          :watched="watches"
          @watched="load().then(reloadGraphs)"
        />
      </template>
    </div>

    <form v-if="settings" class="box price-watch-settings" @submit.prevent="saveSettings">
      <h2 class="subtitle">Alerts &amp; check intervals</h2>
      <p class="alert-channels"
        >Alerts go to your notification channels (Gotify, ntfy, Matrix, Telegram):
        <strong>{{ settings.channels ? settings.channels + ' on' : 'none set up yet' }}</strong> ·
        <router-link to="/settings">Settings → Notifications</router-link></p
      >
      <hr />
      <p class="mb-3"
        >Hours between checks, per price source (0.25 h to 7 days = 168 h). Germany and Austria change prices many times a day;
        Slovenia's and Croatia's regulated prices usually every two weeks. One request checks one station, so fewer is kinder to the
        free services.</p
      >
      <b-field v-for="k in kinds" :key="k" :label="settings.labels[k] || k" :message="`default ${every(settings.defaults[k])}`" horizontal>
        <b-input v-model.number="hours[k]" type="number" min="0.25" max="168" step="0.25" :class="`interval-${k}`"></b-input>
      </b-field>
      <b-button native-type="submit" type="is-primary">Save</b-button>
    </form>
  </Layout>
</template>
