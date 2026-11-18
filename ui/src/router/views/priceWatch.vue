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
export default {
  components: { Layout, StationPicker, PriceChart, PriceHoursChart },
  page: { title: 'Price watch' },
  data() {
    return {
      watches: [],
      loading: true,
      checking: false,
      vehicleId: '',
      days: 30,
      graphs: {}, // watch id → { history, select } while its graph is shown
      settings: null,
      gotify: { url: '', token: '' },
      hours: {}, // kind → hours between checks (form)
      gotifyTest: null,
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
    vehicle() {
      return this.fuelVehicles.find((v) => v.id === this.vehicleId) || this.fuelVehicles[0]
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
      axios
        .get('/api/me/priceWatches')
        .then(({ data }) => (this.watches = data.map(this.toModel)))
        .finally(() => (this.loading = false))
    },
    loadSettings() {
      axios.get('/api/me/priceWatchSettings').then(({ data }) => this.applySettings(data))
    },
    applySettings(data) {
      this.settings = data
      this.gotify = { url: data.gotifyUrl || '', token: '' }
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
          if (this.graphs[w.id]) this.fetchHistory(w.id) // a new grade starts a new history
          this.toast('Saved')
        })
        .catch((ex) => this.toast(this.errorText(ex), false))
    },
    remove(w) {
      this.$buefy.dialog.confirm({
        title: 'Stop watching',
        message: `${w.name}: its price history is deleted too.`,
        confirmText: 'Delete',
        type: 'is-danger',
        onConfirm: () => axios.delete(`/api/me/priceWatches/${w.id}`).then(() => this.load()),
      })
    },
    saveSettings() {
      const intervals = {}
      for (const k in this.hours) intervals[k] = Math.round(this.hours[k] * 60)
      axios
        .post('/api/me/priceWatchSettings', { gotifyUrl: this.gotify.url, gotifyToken: this.gotify.token, intervals })
        .then(({ data }) => {
          this.applySettings(data)
          this.toast('Saved')
        })
        .catch((ex) => this.toast(this.errorText(ex), false))
    },
    removeToken() {
      this.gotify.token = '-'
      this.saveSettings()
    },
    testGotify() {
      this.gotifyTest = null
      axios
        .post('/api/me/priceWatchSettings/test')
        .then(({ data }) => (this.gotifyTest = data))
        .catch((ex) => (this.gotifyTest = { ok: false, error: this.errorText(ex) }))
    },
    every(minutes) {
      return minutes >= 1440 && minutes % 1440 === 0 ? `${minutes / 1440} d` : minutes >= 60 ? `${+(minutes / 60).toFixed(2)} h` : `${minutes} min`
    },
    toggle(w) {
      if (this.graphs[w.id]) return this.$delete(this.graphs, w.id)
      this.$set(this.graphs, w.id, { history: null, select: false })
      this.fetchHistory(w.id)
    },
    fetchHistory(id) {
      axios
        .get(`/api/me/priceWatches/${id}/history`, { params: { days: this.days } })
        .then(({ data }) => this.graphs[id] && (this.graphs[id].history = data))
        .catch((ex) => this.toast(this.errorText(ex), false))
    },
    reloadGraphs() {
      for (const id in this.graphs) this.fetchHistory(id)
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
      >Favourite stations: the price is checked in the background, every change is kept for the graph, and you get a Gotify
      message when it drops to your limit (once, again after it went above).</p
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
          </b-select>
          <b-button size="is-small" icon-left="sync" :loading="checking" :disabled="!watches.length" class="check-now" @click="checkNow"
            >Check now</b-button
          >
        </div>
      </div>
      <p v-if="!loading && !watches.length" class="has-text-grey no-watches"
        >None yet: search below and press <strong>Watch</strong> on a station.</p
      >
      <div
        v-for="(w, i) in watches"
        :key="w.id"
        class="price-watch py-3"
        :style="i ? 'border-top: 1px solid rgba(128, 128, 128, 0.2)' : ''"
      >
        <div class="is-flex is-justify-content-space-between is-align-items-start" style="gap: 0.5rem">
          <div>
            <strong>{{ w.name }}</strong> <b-tag v-if="w.alerted" type="is-success" size="is-small">at or below limit</b-tag><br />
            <small>{{ w.address }}</small><br />
            <small class="has-text-grey"
              >{{ settings && settings.labels[w.kind] }} · every {{ settings ? every(settings.intervals[w.kind]) : '' }}
              <template v-if="w.checkedAt"> · checked {{ fmt(w.checkedAt) }}</template></small
            >
            <p v-if="w.lastError" class="help is-danger watch-error">{{ w.lastError }}</p>
          </div>
          <div class="has-text-right" style="white-space: nowrap">
            <span class="is-size-5 has-text-weight-bold last-price" :class="{ 'has-text-success': w.alerted }"
              >{{ w.lastPrice ? w.lastPrice.toFixed(3) : '–' }}</span
            >
            <small> {{ me ? me.currency : '' }}</small>
          </div>
        </div>
        <form class="is-flex mt-2" style="gap: 0.5rem; flex-wrap: wrap; align-items: flex-end" @submit.prevent="save(w)">
          <b-field label="Grade" custom-class="is-small mb-1" class="mb-0">
            <b-select v-model="w.grade" size="is-small">
              <option v-for="(label, g) in grades" :key="g" :value="g">{{ label }}</option>
            </b-select>
          </b-field>
          <b-field label="Alert at or below" custom-class="is-small mb-1" class="mb-0">
            <b-input v-model="w.belowText" size="is-small" placeholder="e.g. 1.65" inputmode="decimal" class="below" style="width: 7rem"></b-input>
          </b-field>
          <b-button size="is-small" native-type="submit" type="is-primary" class="save-watch">Save</b-button>
          <b-button size="is-small" :class="{ 'is-active': graphs[w.id] }" class="show-graph" @click="toggle(w)">Graph</b-button>
          <b-button size="is-small" type="is-danger" outlined icon-left="trash" class="delete-watch" @click="remove(w)"></b-button>
        </form>
        <div v-if="graphs[w.id] && graphs[w.id].history" class="mt-3 price-graph">
          <p v-if="!graphs[w.id].history.prices.length" class="has-text-grey">No prices in this period yet.</p>
          <template v-else>
            <div class="is-flex is-align-items-center mb-1" style="gap: 0.5rem; flex-wrap: wrap">
              <b-button
                size="is-small"
                :type="graphs[w.id].select ? 'is-primary' : ''"
                class="select-period"
                @click="graphs[w.id].select = !graphs[w.id].select"
                >Select period</b-button
              >
              <b-button size="is-small" class="reset-zoom" @click="resetZoom(w.id)">Reset zoom</b-button>
              <small class="has-text-grey">{{
                graphs[w.id].select ? 'Drag over a period to zoom into it' : 'Wheel or pinch to zoom, drag to move'
              }}</small>
            </div>
            <PriceChart
              :ref="'chart-' + w.id"
              :history="graphs[w.id].history"
              :below="w.below"
              :select="graphs[w.id].select"
              style="height: 240px"
            />
            <PriceHoursChart :history="graphs[w.id].history" class="mt-3 price-hours" style="height: 160px" />
            <p class="help">Cheapest hours in green: the average price per hour of the day in the shown period.</p>
          </template>
        </div>
      </div>
    </div>

    <div class="box add-stations">
      <h2 class="subtitle">Add stations</h2>
      <p v-if="!fuelVehicles.length" class="has-text-grey">Add a petrol or diesel vehicle first: its fuel grades pick the prices.</p>
      <template v-else>
        <b-field v-if="fuelVehicles.length > 1" label="Grades of">
          <b-select v-model="vehicleId" :placeholder="vehicle.nickname">
            <option v-for="v in fuelVehicles" :key="v.id" :value="v.id">{{ v.nickname }}</option>
          </b-select>
        </b-field>
        <StationPicker :key="vehicle.id" :vehicle="vehicle" @watched="load" />
      </template>
    </div>

    <form v-if="settings" class="box price-watch-settings" @submit.prevent="saveSettings">
      <h2 class="subtitle">Alerts &amp; check intervals</h2>
      <b-field label="Gotify URL" message="e.g. https://gotify.example.com">
        <b-input v-model="gotify.url" type="url" class="gotify-url"></b-input>
      </b-field>
      <b-field
        label="Gotify app token"
        :message="settings.gotifyTokenSet ? 'A token is saved; leave empty to keep it' : 'Gotify → Apps → create an app → its token'"
      >
        <b-input v-model="gotify.token" type="password" password-reveal class="gotify-token" autocomplete="off"></b-input>
      </b-field>
      <div class="buttons">
        <b-button native-type="submit" type="is-primary" class="save-settings">Save</b-button>
        <b-button :disabled="!settings.gotifyTokenSet" class="test-gotify" @click="testGotify">Send test message</b-button>
        <b-button v-if="settings.gotifyTokenSet" type="is-danger" outlined @click="removeToken">Remove token</b-button>
      </div>
      <p v-if="gotifyTest" class="help gotify-test" :class="gotifyTest.ok ? 'is-success' : 'is-danger'">{{
        gotifyTest.ok ? 'Sent: check Gotify.' : gotifyTest.error
      }}</p>
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
