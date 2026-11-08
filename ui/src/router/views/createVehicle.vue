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
      authError: null,
      tryingToCreate: false,
      showMore: false,
      myVehicles: [],
      vehicleModel: {},
      tankSuggestion: null,
    }
  },

  computed: {
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
        .get(`/api/vehicles/${this.vehicle.id}/tankSuggestion`)
        .then(({ data }) => {
          if (data.samples >= 2 && data.tankCapacity > 0) this.tankSuggestion = data
        })
        .catch(() => {})
    }
  },
  methods: {
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
      <b-field label="Fuel grade for station prices" message="Which price to show for nearby stations">
        <b-select v-model="vehicleModel.fuelGrade" expanded>
          <option value="">From the fuel type (petrol → E5, diesel → Diesel)</option>
          <option value="e5">Super E5</option>
          <option value="e10">Super E10</option>
          <option value="diesel">Diesel</option>
        </b-select>
      </b-field>
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
