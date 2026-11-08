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
      return fuelSubtypes(this.selectedVehicle.fuelType, this.fillupModel.fuelSubType)
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
          odoReading: d.odoReading,
          fuelQuantity: d.fuelQuantity,
          date: new Date(d.date),
          isTankFull: d.isTankFull,
        })
        this.fillupTime = format(new Date(d.date), 'HH:mm')
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
      <b-field label="Quantity*" addons>
        <b-input v-model.number="fillupModel.fuelQuantity" type="number" step=".001" min="0" expanded required></b-input>
        <b-select v-model="fillupModel.fuelUnit" placeholder="Fuel Unit" required>
          <option v-for="(option, key) in fuelUnitMasters" :key="key" :value="key">
            {{ option.long }}
          </option>
        </b-select>
      </b-field>
      <StationPicker :vehicle="selectedVehicle" :detection="detection" @pick="pickStation" />
      <b-field :label="'Price per ' + vehicle.fuelUnitDetail.short + '*'"
        ><p class="control">
          <span class="button is-static">{{ me.currency }}</span>
        </p>
        <b-input v-model.number="fillupModel.perUnitPrice" type="number" min="0" step=".001" expanded required></b-input>
      </b-field>
      <b-field label="Total Amount Paid">
        <p class="control">
          <span class="button is-static">{{ me.currency }}</span>
        </p>
        <b-input v-model.number="fillupModel.totalAmount" type="number" min="0" step=".001" expanded required></b-input>
      </b-field>
      <b-field label="Odometer Reading" :message="lastOdoReading ? `Last fill-up: ${lastOdoReading} ${me.distanceUnitDetail.short}` : ''">
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
