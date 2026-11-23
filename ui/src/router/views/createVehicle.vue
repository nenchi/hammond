<script>
import Layout from '@layouts/main.vue'
import { mapState } from 'vuex'
import axios from 'axios'

export default {
  page: {
    title: 'Create Vehicle',
  },
  components: { Layout },
  props: {
    vehicle: {
      type: Object,
      required: false,
      default: function() {
        return {}
      },
    },
  },
  data() {
    return {
      photoUrl: (this.vehicle && this.vehicle.photoUrl) || '',
      photoBusy: false,
      gaugeEdit: null, // litres per segment while setting the gauge by hand
      photoShown: !(this.vehicle && this.vehicle.photoHidden),
      entityCheck: null, // { ok, text } from "Check" next to the odometer entity
      entityChecking: false,
      authError: null,
      tryingToCreate: false,
      showMore: false,
      myVehicles: [],
      vehicleModel: {},
      tankSuggestion: null,
      gauge: null,
      gradeChoices: [
        { value: 'e10', text: '95 E10' },
        { value: 'e5', text: '95 (E5)' },
        { value: 'diesel', text: 'Diesel' },
        { value: 'plus', text: '98/100 (not in DE/AT)' },
        { value: 'diesel_premium', text: 'Premium Diesel (not in DE/AT)' },
        { value: 'lpg', text: 'LPG (not in DE/AT)' },
      ],
    }
  },

  computed: {
    // "e10,e5" ⇄ checkboxes; order = display order (E10 before E5)
    acceptedGrades: {
      get() {
        return (this.vehicleModel.fuelGrade || '').split(',').filter(Boolean)
      },
      set(list) {
        this.vehicleModel.fuelGrade = this.gradeChoices.map((g) => g.value).filter((v) => list.includes(v)).join(',')
      },
    },
    tankUnit() {
      const u = this.fuelUnitMasters && this.fuelUnitMasters[this.vehicleModel.fuelUnit]
      return u ? u.short : ''
    },
    ...mapState('users', ['me']),
    ...mapState('vehicles', ['fuelUnitMasters', 'fuelTypeMasters', 'vehicles']),
  },
  watch: {},
  mounted() {
    if (!this.vehicle) {
      this.vehicleModel = this.getEmptyVehicle()
    } else {
      this.vehicleModel = this.getEmptyVehicle(this.vehicle)
    }
    this.myVehicles = this.vehicles
    if (this.vehicle && this.vehicle.id) {
      axios
        .get(`/api/vehicles/${this.vehicle.id}/gauge`)
        .then(({ data }) => (this.gauge = data))
        .catch(() => {})
      axios
        .get(`/api/vehicles/${this.vehicle.id}/tankSuggestion`)
        .then(({ data }) => {
          if (data.samples >= 2 && data.tankCapacity > 0) this.tankSuggestion = data
        })
        .catch(() => {})
    }
  },
  methods: {
    // car photo: the phone offers the camera or the gallery; origin = this address, for Home Assistant's link
    uploadPhoto(e) {
      const file = e.target.files && e.target.files[0]
      e.target.value = ''
      if (!file) return
      const form = new FormData()
      form.append('file', file)
      form.append('origin', window.location.origin)
      this.photoBusy = true
      axios
        .put(`/api/vehicles/${this.vehicle.id}/photo`, form)
        .then(({ data }) => {
          this.photoUrl = data.photoUrl
          this.$store.dispatch('vehicles/fetchVehicles')
          this.$buefy.toast.open({ message: 'Photo saved', type: 'is-success', duration: 3000 })
        })
        .catch((ex) => {
          const msg = (ex.response && ex.response.data && ex.response.data.error) || ex.message
          this.$buefy.toast.open({ message: msg, type: 'is-danger', duration: 5000, position: 'is-bottom' })
        })
        .finally(() => (this.photoBusy = false))
    },
    // gauge by hand: litres per bar, bottom first; null = back to the learned values
    saveGauge(litres) {
      axios
        .put(`/api/vehicles/${this.vehicle.id}/gauge/manual`, { segmentLitres: litres })
        .then(({ data }) => {
          this.gauge = data
          this.gaugeEdit = null
          this.$buefy.toast.open({ message: litres ? 'Gauge saved' : 'Using the learned gauge', type: 'is-success', duration: 3000 })
        })
        .catch((ex) => {
          const msg = (ex.response && ex.response.data && ex.response.data.error) || ex.message
          this.$buefy.toast.open({ message: msg, type: 'is-danger', duration: 5000, position: 'is-bottom' })
        })
    },
    setPhotoShown(shown) {
      axios
        .put(`/api/vehicles/${this.vehicle.id}/photoHidden`, { hidden: !shown })
        .then(() => this.$store.dispatch('vehicles/fetchVehicles'))
    },
    removePhoto() {
      axios.delete(`/api/vehicles/${this.vehicle.id}/photo`).then(() => {
        this.photoUrl = ''
        this.$store.dispatch('vehicles/fetchVehicles')
      })
    },
    // asks your Home Assistant for the entity right away (before saving)
    checkEntity() {
      this.entityChecking = true
      this.entityCheck = null
      axios
        .get('/api/me/homeAssistant/entity', { params: { entity: this.vehicleModel.odometerEntity } })
        .then(({ data }) => {
          const min = Math.max(0, Math.round((Date.now() - new Date(data.updatedAt)) / 60000))
          const ago = min < 60 ? `${min} min` : min < 2880 ? `${Math.round(min / 60)} h` : `${Math.round(min / 1440)} days`
          this.entityCheck = { ok: true, text: `✓ Found: ${data.value} ${data.unit || ''}, updated ${ago} ago` }
        })
        .catch((ex) => {
          const msg = (ex.response && ex.response.data && ex.response.data.error) || ex.message
          this.entityCheck = { ok: false, text: '✗ ' + msg }
        })
        .finally(() => (this.entityChecking = false))
    },
    resetGauge() {
      this.$buefy.dialog.confirm({
        title: 'Reset gauge calibration',
        message: 'Forget what was learned from earlier fill-ups and start linear again?',
        confirmText: 'Reset',
        type: 'is-danger',
        onConfirm: () =>
          axios
            .post(`/api/vehicles/${this.vehicle.id}/gauge/reset`)
            .then(() => axios.get(`/api/vehicles/${this.vehicle.id}/gauge`))
            .then(({ data }) => (this.gauge = data)),
      })
    },
    getEmptyVehicle(veh) {
      if (!veh || !veh.id) {
        return {
          fuelUnit: null,
          fuelType: null,
          registration: '',
          nickname: '',
          engineSize: null,
          tankCapacity: null,
          fuelGrade: '',
          gaugeSegments: 4,
          odometerEntity: '',
          evccVehicle: '',
          make: '',
          model: '',
          yearOfManufacture: null,
        }
      } else {
        return {
          fuelUnit: veh.fuelUnit,
          fuelType: veh.fuelType,
          registration: veh.registration,
          nickname: veh.nickname,
          engineSize: veh.engineSize,
          tankCapacity: veh.tankCapacity || null,
          fuelGrade: veh.fuelGrade || '',
          gaugeSegments: veh.gaugeSegments || 4,
          odometerEntity: veh.odometerEntity || '',
          evccVehicle: veh.evccVehicle || '',
          make: veh.make,
          model: veh.model,
          yearOfManufacture: veh.yearOfManufacture,
        }
      }
    },
    createVehicle() {
      this.tryingToCreate = true
      this.vehicleModel.userId = this.me.id
      if (this.vehicle.id) {
        axios
          .put(`/api/vehicles/${this.vehicle.id}`, this.vehicleModel)
          .then((data) => {
            this.$buefy.toast.open({
              message: 'Vehicle Updated Successfully',
              type: 'is-success',
              duration: 3000,
            })
            // this.vehicleModel = this.getEmptyVehicle()
          })
          .catch((ex) => {
            this.$buefy.toast.open({
              duration: 5000,
              message: ex.message,
              position: 'is-bottom',
              type: 'is-danger',
            })
          })
          .finally(() => {
            this.tryingToCreate = false
          })
      } else {
        axios
          .post(`/api/vehicles`, this.vehicleModel)
          .then((data) => {
            this.$buefy.toast.open({
              message: 'Vehicle Created Successfully',
              type: 'is-success',
              duration: 3000,
            })
            this.vehicleModel = this.getEmptyVehicle()
          })
          .catch((ex) => {
            this.$buefy.toast.open({
              duration: 5000,
              message: ex.message,
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
    <div class="columns">
      <div class="column is-three-quarters">
        <h1 class="title">Create Vehicle</h1>
      </div>
      <div class="column is-one-quarter">
        <router-link tag="b-button" type="is-primary" to="/">
          Back to Vehicle
        </router-link>
      </div>
    </div>
    <div v-if="vehicle && vehicle.id" class="box vehicle-photo mb-4">
      <p class="heading">Photo</p>
      <img v-if="photoUrl" :src="photoUrl" alt="Car photo" style="max-height: 200px; border-radius: 6px; display: block" />
      <p v-else class="help mb-2">No photo yet. Shown on your cars and in Home Assistant (over MQTT).</p>
      <div class="buttons mt-2">
        <label class="button is-primary" :class="{ 'is-loading': photoBusy }">
          <input class="photo-input" type="file" accept="image/*" style="display: none" @change="uploadPhoto" />
          {{ photoUrl ? 'Change photo' : 'Take or choose a photo' }}
        </label>
        <b-button v-if="photoUrl" type="is-danger" outlined @click="removePhoto">Remove</b-button>
      </div>
      <b-switch v-if="photoUrl" v-model="photoShown" class="photo-shown" @input="setPhotoShown"
        >Show in hammond (off: only for Home Assistant)</b-switch
      >
    </div>
    <form @submit.prevent="createVehicle">
      <b-field label="Nickname*">
        <b-input v-model="vehicleModel.nickname" type="text" expanded required></b-input>
      </b-field>
      <b-field label="Registration*">
        <b-input v-model="vehicleModel.registration" type="text" expanded required></b-input>
      </b-field>
      <b-field label="Fuel Type*">
        <b-select v-model.number="vehicleModel.fuelType" placeholder="Fuel Type" required expanded>
          <option v-for="(option, key) in fuelTypeMasters" :key="key" :value="key">
            {{ option.long }}
          </option>
        </b-select>
      </b-field>

      <b-field label="Fuel Unit*">
        <b-select v-model.number="vehicleModel.fuelUnit" placeholder="Fuel Unit" required expanded>
          <option v-for="(option, key) in fuelUnitMasters" :key="key" :value="key">
            {{ option.long }}
          </option>
        </b-select>
      </b-field>

      <b-field
        :label="`Tank size (${tankUnit || 'litres'})`"
        message="Used to estimate fill-ups detected from the fuel level (Home Assistant etc.)"
      >
        <b-input v-model.number="vehicleModel.tankCapacity" type="number" min="0" step=".1" expanded></b-input>
      </b-field>
      <p v-if="tankSuggestion && tankSuggestion.tankCapacity !== vehicleModel.tankCapacity" class="help mb-3 tank-suggestion">
        Suggested from {{ tankSuggestion.samples }} confirmed detections: {{ tankSuggestion.tankCapacity }} {{ tankUnit }} ·
        <a @click="vehicleModel.tankCapacity = tankSuggestion.tankCapacity">use it</a>
      </p>
      <b-field
        label="Accepted grades for station prices"
        message="Prices of all ticked grades are shown; the first ticked one is preferred. None = from the fuel type."
      >
        <div class="fuel-grades">
          <b-checkbox v-for="g in gradeChoices" :key="g.value" v-model="acceptedGrades" :native-value="g.value">{{ g.text }}</b-checkbox>
        </div>
      </b-field>
      <b-field
        label="Home Assistant odometer entity"
        message="Pre-fills new fill-ups with the car's current odometer, e.g. sensor.a180_odometer (Settings → Home Assistant)"
      >
        <b-input v-model.trim="vehicleModel.odometerEntity" pattern="[a-z0-9_]+\.[a-z0-9_]+" maxlength="100" :has-counter="false"
          placeholder="sensor.…_odometer" expanded></b-input>
        <p class="control">
          <b-button class="check-entity" :loading="entityChecking" :disabled="!vehicleModel.odometerEntity" @click="checkEntity">Check</b-button>
        </p>
      </b-field>
      <p v-if="entityCheck" class="help mb-3 entity-check" :class="entityCheck.ok ? 'is-success' : 'is-danger'">{{ entityCheck.text }}</p>
      <b-field
        v-if="vehicleModel.fuelType === 4"
        label="EVCC vehicle"
        message="The vehicle's name (title) in EVCC: its charging sessions are imported (Settings → EVCC)"
      >
        <b-input v-model.trim="vehicleModel.evccVehicle" maxlength="100" :has-counter="false"></b-input>
      </b-field>
      <b-field
        label="Gauge segments"
        message="How many parts the fuel gauge has (4 = quarters). Each part learns its own litres per % from your confirmed detections and, with Trips from Home Assistant, from your drives (the car's trip computer, else km × your average consumption)."
      >
        <b-input v-model.number="vehicleModel.gaugeSegments" type="number" min="1" max="10" step="1"></b-input>
      </b-field>
      <div v-if="gauge" class="box gauge-calibration mb-4">
        <p class="heading">
          Gauge calibration ·
          <span v-if="gauge.manual" class="has-text-info">set by hand</span>
          <span v-else-if="gauge.calibrated" class="has-text-success"
            >learned from {{ gauge.samples }} fill-ups and {{ gauge.driveSamples }} drives</span
          >
          <span v-else>linear (needs 2 confirmed detections or drives from Trips, has {{ gauge.samples + gauge.driveSamples }})</span>
        </p>
        <div class="is-flex is-align-items-flex-end" style="gap: 4px; height: 90px">
          <div
            v-for="(litres, i) in gauge.segmentLitres"
            :key="i"
            class="gauge-segment has-text-centered"
            :title="`${(i * 100) / gauge.segments}–${((i + 1) * 100) / gauge.segments} %: ${litres} ${tankUnit}`"
            :style="{
              flex: 1,
              height: Math.max(12, (litres / Math.max(...gauge.segmentLitres)) * 70) + 'px',
              background: 'rgba(57, 162, 117, 0.6)',
              borderRadius: '3px 3px 0 0',
            }"
          >
            <small>{{ litres }}</small>
          </div>
        </div>
        <div class="is-flex" style="gap: 4px">
          <small v-for="i in gauge.segments" :key="i" class="has-text-centered" style="flex: 1"
            >{{ ((i - 1) * 100) / gauge.segments }}–{{ (i * 100) / gauge.segments }} %</small
          >
        </div>
        <p class="help"
          >Litres per segment, empty (0 %) on the left. Sum {{ gauge.tankCapacity }} {{ tankUnit }}.
          <span v-if="gauge.driveSamples"
            >Drives: litres from the car's trip computer{{
              gauge.litresPerKm ? `, else ${(gauge.litresPerKm * 100).toFixed(1)} ${tankUnit}/100 (average of your fill-ups)` : ''
            }}.</span
          >
          <a v-if="gauge.samples" class="has-text-danger reset-gauge" @click="resetGauge">Reset</a></p
        >
        <div v-if="gaugeEdit" class="gauge-edit">
          <div class="is-flex" style="gap: 4px">
            <b-input
              v-for="(l, i) in gaugeEdit"
              :key="i"
              v-model.number="gaugeEdit[i]"
              type="number"
              step="0.1"
              min="0.1"
              size="is-small"
              style="flex: 1"
              :aria-label="`Litres ${(i * 100) / gauge.segments}–${((i + 1) * 100) / gauge.segments} %`"
            />
          </div>
          <p class="help">
            {{ tankUnit }} per segment, empty on the left. Sum {{ gaugeEdit.reduce((a, b) => a + (Number(b) || 0), 0).toFixed(1) }}
            {{ tankUnit }}.
          </p>
          <div class="buttons mt-2">
            <b-button size="is-small" type="is-primary" class="save-gauge" @click="saveGauge(gaugeEdit.map(Number))">Save</b-button>
            <b-button size="is-small" @click="gaugeEdit = null">Cancel</b-button>
          </div>
        </div>
        <div v-else class="buttons mt-2">
          <b-button size="is-small" class="edit-gauge" @click="gaugeEdit = gauge.segmentLitres.slice()">Set manually</b-button>
          <b-button v-if="gauge.manual" size="is-small" class="use-learned" @click="saveGauge(null)"
            >Use learned ({{ gauge.samples + gauge.driveSamples }} samples)</b-button
          >
        </div>
      </div>
      <b-field label="Make / Company*">
        <b-input v-model="vehicleModel.make" type="text" required expanded></b-input>
      </b-field>
      <b-field label="Model*">
        <b-input v-model="vehicleModel.model" type="text" required expanded></b-input>
      </b-field>
      <b-field label="Year Of Manufacture">
        <b-input v-model.number="vehicleModel.yearOfManufacture" type="number" expanded number></b-input>
      </b-field>
      <b-field label="Engine Size (in cc)">
        <b-input v-model.number="vehicleModel.engineSize" type="number" expanded number></b-input>
      </b-field>

      <br />
      <b-field>
        <b-button tag="input" native-type="submit" :disabled="tryingToCreate" type="is-primary" label="Create Vehicle" expanded>
          <BaseIcon v-if="tryingToCreate" name="sync" spin />
        </b-button>
        <p v-if="authError">
          There was an error logging in to your account.
        </p>
      </b-field>
    </form>
  </Layout>
</template>
