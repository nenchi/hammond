<script>
import Layout from '@layouts/main.vue'
import QuickEntryDisplay from '@components/quickEntryDisplay.vue'
import StationPicker from '@components/stationPicker.vue'
import store from '@state/store'

import { mapState } from 'vuex'
import axios from 'axios'
import { round } from 'lodash'
import { format } from 'date-fns'
import { timeOf } from '@utils/format-date'
import { fuelSubtypes, defaultFuelSubtype } from '@utils/fuel-grades'
import { gapLimit } from '@utils/odometer'

export default {
  page: {
    title: 'Create Fillup',
  },
  components: { Layout, QuickEntryDisplay, StationPicker },
  props: {
    vehicle: {
      type: Object,
      required: true,
    },
    fillup: {
      type: Object,
      required: false,
      default: function() {
        return {}
      },
    },
  },
  data() {
    return {
      authError: null,
      tryingToCreate: false,
      showMore: false,
      quickEntry: null,
      users: [],
      fuelSubTypes: [],
      selectedVehicle: this.vehicle,
      fillupModel: this.fillup,
      processQuickEntry: false,
      lastOdoReading: null,
      // the car's odometer from Home Assistant: { value, unit, updatedAt } or { error }
      haOdometer: null,
      // electric: charging place + end time → price from the place's tariff
      chargingPlaces: [],
      placeId: '',
      chargeEnd: '',
      quote: null,
      quoteError: '',
      quoteLockedKey: '', // inputs a detection came with its own price for (EVCC): not re-priced until changed
      typicalGap: null,
      gapLimit: 1500,
      detection: null,
      // HH:mm next to the date picker (which only sets whole days); combined on save
      fillupTime: '',
    }
  },

  computed: {
    // reactive, with the current vehicle always in it: copied once at mount (upstream), a fresh page load
    // left the required select empty while the vehicles were still loading, and saving silently failed
    myVehicles() {
      const list = this.vehicles || []
      const sel = this.selectedVehicle
      if (!sel || !sel.id) return list
      return list.some((v) => v.id === sel.id) ? list.map((v) => (v.id === sel.id ? sel : v)) : [sel, ...list]
    },
    user() {
      return store.state.auth.currentUser
    },
    ...mapState('users', ['me']),
    ...mapState('vehicles', ['fuelUnitMasters', 'fuelTypeMasters', 'vehicles']),
    subtypeOptions() {
      const custom = ((this.me && this.me.customFuelSubtypes) || '').split('\n').filter(Boolean)
      return fuelSubtypes(this.selectedVehicle.fuelType, this.fillupModel.fuelSubType, custom)
    },
    isElectric() {
      return this.selectedVehicle && this.selectedVehicle.fuelType === 4
    },
    // start/end of the charge from the date, the time and the end time (past midnight = next day)
    chargeTimes() {
      const start = new Date(this.fillupModel.date)
      const [h, m] = (this.fillupTime || '00:00').split(':').map(Number)
      start.setHours(h || 0, m || 0, 0, 0)
      let end = null
      if (this.chargeEnd) {
        end = new Date(start)
        const [eh, em] = this.chargeEnd.split(':').map(Number)
        end.setHours(eh || 0, em || 0, 0, 0)
        if (end <= start) end.setDate(end.getDate() + 1)
      }
      return { start, end }
    },
    quoteKey() {
      return JSON.stringify([this.placeId, this.fillupModel.fuelQuantity, this.chargeTimes.start, this.chargeTimes.end])
    },
    quoteText() {
      const q = this.quote
      if (!q) return ''
      const parts = q.parts.map((p) => `${p.name} ${p.cost.toFixed(2)}`).join(' + ')
      const fee = q.sessionFee ? ` + session ${q.sessionFee.toFixed(2)}` : ''
      const loss = q.billedKWh !== q.kWh ? ` (${q.billedKWh.toFixed(2)} kWh incl. loss)` : ''
      return `${q.place}: ${parts}${fee} = ${q.total.toFixed(2)}${loss}`
    },
    haOdometerHint() {
      const o = this.haOdometer
      if (!o) return ''
      if (o.error) return o.error
      if (Math.round(o.value) !== this.fillupModel.odoReading) return ''
      const min = Math.max(0, Math.round((Date.now() - new Date(o.updatedAt)) / 60000))
      const ago = min < 60 ? `${min} min` : min < 2880 ? `${Math.round(min / 60)} h` : `${Math.round(min / 1440)} days`
      return `From Home Assistant, updated ${ago} ago`
    },
    // soft warning only (typo check), saving still works: back-dated entries and long trips are legit
    odoWarning() {
      const odo = this.fillupModel.odoReading
      if (!this.lastOdoReading || !odo) return ''
      const unit = this.me.distanceUnitDetail.short
      const d = odo - this.lastOdoReading
      if (d < 0) return `Lower than the last fill-up (${this.lastOdoReading} ${unit}). Back-dated entry? Otherwise check the number.`
      if (d > this.gapLimit && !this.fillupModel.hasMissedFillup)
        return `${d} ${unit} since the last fill-up${this.typicalGap ? ` (usually ~${this.typicalGap})` : ''}. Typo? If you skipped an entry, tick "Did you miss the fillup entry before this one?".`
      return ''
    },
    filteredFuelSubtypes() {
      if (!this.fillupModel.fuelSubType) {
        return this.fuelSubTypes
      }
      return this.fuelSubTypes.filter((option) => {
        return option.toLowerCase().indexOf(this.fillupModel.fuelSubType.toLowerCase()) >= 0
      })
    },
  },
  watch: {
    quoteKey() {
      this.scheduleQuote()
    },
    'fillupModel.fuelQuantity': function(old, newOne) {
      this.fillupModel.totalAmount = round(this.fillupModel.fuelQuantity * this.fillupModel.perUnitPrice, 2)
    },
    'fillupModel.perUnitPrice': function(old, newOne) {
      this.fillupModel.totalAmount = round(this.fillupModel.fuelQuantity * this.fillupModel.perUnitPrice, 2)
    },
    quickEntry: function(newOne, old) {
      if (old == null && newOne !== null) {
        this.processQuickEntry = true
      }
    },
  },
  mounted() {
    this.selectedVehicle = this.vehicle
    this.fetchVehicleUsers()
    this.fetchVehicleFuelSubTypes()
    if (this.isElectric) this.loadChargingPlaces()
    if (this.fillup.id) {
      this.fillupTime = timeOf(this.fillup.date)
    } else {
      this.fillupTime = format(new Date(), 'HH:mm')
    }
    if (!this.fillup.id) {
      this.fillupModel = this.getEmptyFillup()
      this.fillupModel.userId = this.me.id
      this.prefillFromLastFillup()
      if (this.$route.query.detection) this.prefillFromDetection(this.$route.query.detection)
      else this.prefillFromHomeAssistant() // a detection brings its own odometer
    }
  },
  methods: {
    formatDate(date) {
      const finalFormat = this.me.dateFormat ? this.me.dateFormat : 'MM/dd/yyyy'
      return format(date, finalFormat)
    },
    fetchVehicleUsers() {
      store
        .dispatch('vehicles/fetchUsersByVehicleId', { vehicleId: this.selectedVehicle.id })
        .then((data) => {
          this.users = data
        })
        .catch((err) => console.log(err))
    },
    fetchVehicleFuelSubTypes() {
      store
        .dispatch('vehicles/fetchFuelSubtypesByVehicleId', { vehicleId: this.selectedVehicle.id })
        .then((data) => {
          this.fuelSubTypes = data
        })
        .catch((err) => console.log(err))
    },
    pickStation({ name, price, grade }) {
      this.fillupModel.fillingStation = name
      if (price) this.fillupModel.perUnitPrice = price
      if (grade) this.fillupModel.fuelSubType = grade
      this.showMore = true
    },
    // "Complete" on a detected fill-up (Home Assistant etc.): odometer, quantity, date and tank-full from
    // the detection; saving links it (detectionId) so it's marked confirmed
    prefillFromDetection(id) {
      store.dispatch('vehicles/fetchDetections').then((list) => {
        const d = (list || []).find((x) => x.id === id && x.vehicleId === this.selectedVehicle.id)
        if (!d) return
        this.detection = d
        Object.assign(this.fillupModel, {
          detectionId: d.id,
          odoReading: d.odoReading || '',
          fuelQuantity: d.fuelQuantity,
          date: new Date(d.date),
          isTankFull: d.isTankFull,
        })
        this.fillupTime = format(new Date(d.date), 'HH:mm')
        if (d.chargeEnd) this.chargeEnd = format(new Date(d.chargeEnd), 'HH:mm')
        if (d.station) this.fillupModel.fillingStation = d.station
        if (d.perUnitPrice) this.fillupModel.perUnitPrice = d.perUnitPrice
        if (!d.odoReading) this.prefillFromHomeAssistant() // EVCC without the car's odometer
        this.pickPlace(d)
        if (d.perUnitPrice) this.$nextTick(() => (this.quoteLockedKey = this.quoteKey))
      })
    },
    loadChargingPlaces() {
      axios.get('/api/chargingPlaces').then(({ data }) => {
        this.chargingPlaces = data
        this.pickPlace(this.detection)
        // places can arrive after the detection: keep its own price (EVCC) for these inputs too
        if (this.detection && this.detection.perUnitPrice) this.$nextTick(() => (this.quoteLockedKey = this.quoteKey))
      })
    },
    // the detection's place (EVCC) or the one within its radius of the detection, else the only one
    pickPlace(d) {
      if (this.placeId || !this.chargingPlaces.length) return
      const near = (p) => {
        if (!d || d.lat == null || p.lat == null) return false
        const dy = (d.lat - p.lat) * 111320
        const dx = (d.lng - p.lng) * 111320 * Math.cos((p.lat * Math.PI) / 180)
        return Math.hypot(dx, dy) <= (p.radiusM || 200)
      }
      const p =
        this.chargingPlaces.find((x) => d && d.station && x.name === d.station) ||
        this.chargingPlaces.find(near) ||
        (!d && this.chargingPlaces.length === 1 ? this.chargingPlaces[0] : null)
      if (p) this.placeId = p.id
    },
    scheduleQuote() {
      clearTimeout(this.quoteTimer)
      this.quoteTimer = setTimeout(this.loadQuote, 400)
    },
    loadQuote() {
      this.quoteError = ''
      const kWh = Number(this.fillupModel.fuelQuantity)
      if (!this.isElectric || !this.placeId || !(kWh > 0)) {
        this.quote = null
        return
      }
      if (this.quoteKey === this.quoteLockedKey) return
      const place = this.chargingPlaces.find((p) => p.id === this.placeId)
      const { start, end } = this.chargeTimes
      axios
        .post(`/api/chargingPlaces/${this.placeId}/quote`, { start, end, kWh })
        .then(({ data }) => {
          this.quote = data
          this.fillupModel.perUnitPrice = data.perUnitPrice
          if (place) {
            this.fillupModel.fillingStation = place.name
            this.showMore = true // show what was filled in
          }
        })
        .catch((ex) => {
          this.quote = null
          this.quoteError = (ex.response && ex.response.data && ex.response.data.error) || ex.message
        })
    },
    // last price, station and fuel subtype (setting "pre-fill"); the odometer only as a hint, a copied
    // value that isn't changed would make the mileage wrong
    prefillFromLastFillup() {
      axios
        .get(`/api/vehicles/${this.selectedVehicle.id}/fillups`)
        .then(({ data }) => {
          // highest odometer, not the first by date: same-day or back-dated entries sort ambiguously
          const last = (data || []).reduce((a, b) => (!a || b.odoReading > a.odoReading ? b : a), null)
          if (!last || this.fillup.id) return
          this.lastOdoReading = last.odoReading
          ;({ median: this.typicalGap, limit: this.gapLimit } = gapLimit(data))
          if (this.me.prefillFillup === false) return
          if (!this.fillupModel.perUnitPrice) this.fillupModel.perUnitPrice = last.perUnitPrice
          if (!this.fillupModel.fillingStation && last.fillingStation) {
            this.fillupModel.fillingStation = last.fillingStation
            this.showMore = true // show what was pre-filled
          }
          if (!this.fillupModel.fuelSubType) this.fillupModel.fuelSubType = last.fuelSubType || defaultFuelSubtype(this.selectedVehicle)
        })
        .catch((err) => console.log(err))
    },
    // the car's current odometer (Admin → Home Assistant + the vehicle's entity); only into an empty field
    prefillFromHomeAssistant() {
      axios
        .get(`/api/vehicles/${this.selectedVehicle.id}/odometer`)
        .then(({ data }) => {
          if (!data.configured) return
          const mine = (this.me.distanceUnitDetail.short || '').toLowerCase()
          const theirs = (data.unit || '').toLowerCase()
          // ponytail: no km↔mi conversion, a different unit is only shown
          if (theirs && mine && theirs !== mine) {
            this.haOdometer = { error: `Car reports ${data.value} ${data.unit}, not ${this.me.distanceUnitDetail.short}: not pre-filled.` }
            return
          }
          this.haOdometer = data
          if (!this.fillupModel.odoReading) this.fillupModel.odoReading = Math.round(data.value)
        })
        .catch((ex) => {
          this.haOdometer = { error: 'Home Assistant: ' + ((ex.response && ex.response.data && ex.response.data.error) || ex.message) }
        })
    },
    getEmptyFillup() {
      return {
        vehicleId: this.selectedVehicle.id,
        fuelUnit: this.selectedVehicle.fuelUnit,
        perUnitPrice: null,
        fuelQuantity: null,
        totalAmount: null,
        odoReading: '',
        isTankFull: true,
        hasMissedFillup: false,
        date: new Date(),
        fillingStation: '',
        comments: '',
        userId: '',
        fuelSubType: defaultFuelSubtype(this.selectedVehicle),
      }
    },
    async createFillup() {
      this.tryingToCreate = true
      const when = new Date(this.fillupModel.date)
      const [h, m] = (this.fillupTime || '00:00').split(':').map(Number)
      when.setHours(h || 0, m || 0, 0, 0)
      this.fillupModel.date = when
      this.fillupModel.vehicleId = this.selectedVehicle.id
      //  this.fillupModel.userId = this.me.id
      if (this.fillup.id) {
        axios
          .put(`/api/vehicles/${this.selectedVehicle.id}/fillups/${this.fillup.id}`, this.fillupModel)
          .then((data) => {
            this.$buefy.toast.open({
              message: 'Fillup Updated Successfully',
              type: 'is-success',
              duration: 3000,
            })
            this.fillupModel = this.getEmptyFillup()
            if (this.processQuickEntry) {
              store.dispatch('vehicles/setQuickEntryAsProcessed', { id: this.quickEntry.id }).then((data) => {
                this.quickEntry = null
              })
            }
          })
          .catch((ex) => {
            this.$buefy.toast.open({
              duration: 5000,
              message: ex,
              position: 'is-bottom',
              type: 'is-danger',
            })
          })
          .finally(() => {
            this.tryingToCreate = false
          })
      } else {
        axios
          .post(`/api/vehicles/${this.selectedVehicle.id}/fillups`, this.fillupModel)
          .then((data) => {
            this.$buefy.toast.open({
              message: 'Fillup Created Successfully',
              type: 'is-success',
              duration: 3000,
            })
            this.fillupModel = this.getEmptyFillup()
            if (this.processQuickEntry) {
              store.dispatch('vehicles/setQuickEntryAsProcessed', { id: this.quickEntry.id }).then((data) => {})
            }
            // the server confirmed/merged matching detections: refresh the badge, go back to the list
            store.dispatch('vehicles/fetchDetections').catch(() => {})
            if (this.detection) {
              this.detection = null
              this.$router.push({ name: 'detections' })
            }
          })
          .catch((ex) => {
            this.$buefy.toast.open({
              duration: 5000,
              message: ex,
              position: 'is-bottom',
              type: 'is-danger',
            })
          })
          .finally(() => {
            this.tryingToCreate = false
          })
      }
    },
  },
}
</script>

<template>
  <Layout>
    <div class="has-text-centered">
      <div class="columns">
        <div class="column is-two-thirds">
          <h1 class="title">Create Fillup</h1>
          <h1 class="subtitle">
            {{ [selectedVehicle.nickname, selectedVehicle.registration, selectedVehicle.make, selectedVehicle.model].join(' | ') }}
          </h1>
        </div>
        <div class="column is-one-thirds">
          <QuickEntryDisplay v-model="quickEntry" :user="user" />
        </div>
      </div>
    </div>
    <b-notification v-if="detection" :type="detection.uncertain ? 'is-warning' : 'is-info'" :closable="false" class="detection-note">
      Completing a fill-up detected by <strong>{{ detection.source }}</strong>:
      {{ detection.estimated ? 'estimated' : '' }} {{ detection.fuelQuantity }} {{ selectedVehicle.fuelUnitDetail.short }}
      <span v-if="detection.levelBefore !== null && detection.levelAfter !== null"
        >({{ detection.levelBefore }}% → {{ detection.levelAfter }}%)</span
      >
      at {{ detection.odoReading }}. Check the quantity and add the price.
      <span v-if="detection.uncertain"><br />The estimate may be low: {{ detection.uncertainNote }}.</span>
    </b-notification>
    <form class="" @submit.prevent="createFillup">
      <b-field label="Select a vehicle">
        <b-select v-model="selectedVehicle" placeholder="Vehicle" required expanded :disabled="fillup.id">
          <option v-for="option in myVehicles" :key="option.id" :value="option">
            {{ option.nickname }}
          </option>
        </b-select>
      </b-field>
      <b-field label="Expense by">
        <b-select v-model="fillupModel.userId" placeholder="User" required expanded :disabled="fillup.id">
          <option v-for="option in users" :key="option.userId" :value="option.userId">
            {{ option.name }}
          </option>
        </b-select>
      </b-field>
      <b-field label="Fillup Date">
        <b-datepicker
          v-model="fillupModel.date"
          :date-formatter="formatDate"
          placeholder="Click to select..."
          icon="calendar"
          trap-focus
          :max-date="new Date()"
        >
        </b-datepicker>
      </b-field>
      <b-field label="Time" message="Optional; empty = no time">
        <b-input v-model="fillupTime" type="time" class="fillup-time"></b-input>
      </b-field>
      <b-field label="Fuel Subtype">
        <b-select v-model="fillupModel.fuelSubType" expanded class="fuel-subtype">
          <option value="">–</option>
          <option v-for="[value, text] in subtypeOptions" :key="value" :value="value">{{ text }}</option>
        </b-select>
      </b-field>
      <b-field v-if="isElectric && chargingPlaces.length" grouped group-multiline class="charging">
        <b-field label="Charging place" expanded>
          <b-select v-model="placeId" expanded class="charging-place">
            <option value="">— elsewhere: enter the price —</option>
            <option v-for="p in chargingPlaces" :key="p.id" :value="p.id">{{ p.name }}</option>
          </b-select>
        </b-field>
        <b-field label="Charge end" message="For time-based prices; empty = all at the start time">
          <b-input v-model="chargeEnd" type="time" class="charge-end"></b-input>
        </b-field>
      </b-field>
      <b-field label="Quantity*" addons>
        <b-input v-model.number="fillupModel.fuelQuantity" type="number" step=".001" min="0" expanded required></b-input>
        <b-select v-model="fillupModel.fuelUnit" placeholder="Fuel Unit" required>
          <option v-for="(option, key) in fuelUnitMasters" :key="key" :value="key">
            {{ option.long }}
          </option>
        </b-select>
      </b-field>
      <StationPicker v-if="!isElectric" :vehicle="selectedVehicle" :detection="detection" @pick="pickStation" />
      <b-field :label="'Price per ' + vehicle.fuelUnitDetail.short + '*'"
        ><p class="control">
          <span class="button is-static">{{ me.currency }}</span>
        </p>
        <b-input v-model.number="fillupModel.perUnitPrice" type="number" min="0" step=".0001" expanded required></b-input>
      </b-field>
      <p v-if="quoteText" class="help mb-3 charge-quote">{{ quoteText }}</p>
      <p v-if="quoteError" class="help is-danger mb-3">{{ quoteError }}</p>
      <b-field label="Total Amount Paid">
        <p class="control">
          <span class="button is-static">{{ me.currency }}</span>
        </p>
        <b-input v-model.number="fillupModel.totalAmount" type="number" min="0" step=".001" expanded required></b-input>
      </b-field>
      <b-field
        label="Odometer Reading"
        :type="odoWarning ? 'is-warning' : ''"
        :message="[odoWarning, haOdometerHint, lastOdoReading ? `Last fill-up: ${lastOdoReading} ${me.distanceUnitDetail.short}` : ''].filter(Boolean).join(' · ')"
      >
        <p class="control">
          <span class="button is-static">{{ me.distanceUnitDetail.short }}</span>
        </p>
        <b-input
          v-model.number="fillupModel.odoReading"
          type="number"
          min="0"
          :placeholder="lastOdoReading ? `last: ${lastOdoReading}` : ''"
          expanded
          required
        ></b-input>
      </b-field>
      <b-field>
        <b-checkbox v-model="fillupModel.isTankFull">Did you get a full tank?</b-checkbox>
      </b-field>
      <b-field>
        <b-checkbox v-model="fillupModel.hasMissedFillup">Did you miss the fillup entry before this one?</b-checkbox>
      </b-field>
      <b-field>
        <b-switch v-model="showMore">Fill more details</b-switch>
      </b-field>
      <fieldset v-if="showMore">
        <b-field label="Filling Station Name">
          <b-input v-model="fillupModel.fillingStation" type="text" expanded></b-input>
        </b-field>
        <b-field label="Comments">
          <b-input v-model="fillupModel.comments" type="textarea" expanded></b-input>
        </b-field>
      </fieldset>
      <b-field>
        <b-switch v-if="quickEntry" v-model="processQuickEntry">Mark selected Quick Entry as processed</b-switch>
      </b-field>
      <br />
      <b-field>
        <b-button tag="input" native-type="submit" :disabled="tryingToCreate" type="is-primary" label="Create Fillup" expanded> </b-button>
        <p v-if="authError">
          There was an error logging in to your account.
        </p>
      </b-field>
    </form>
  </Layout>
</template>
