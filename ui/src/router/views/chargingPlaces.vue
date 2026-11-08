<script>
import Layout from '@layouts/main.vue'
import axios from 'axios'

// Charging places for electric cars and how a charge there is priced: price per kWh = the sum of the
// components (each: the first rule that matches month, day type and time, else its default) + a Home
// Assistant price sensor (optional). Example Slovenia: "Energy" with VT/MT, "Network" with the time blocks.
const DAYS = { all: 'Every day', work: 'Working days', free: 'Weekend + holidays' }

export default {
  components: { Layout },
  page: { title: 'Charging places' },
  data() {
    return { places: [], loading: true, days: DAYS, saving: '' }
  },
  mounted() {
    this.load()
  },
  methods: {
    load() {
      axios
        .get('/api/chargingPlaces')
        .then(({ data }) => (this.places = data.map(this.toModel)))
        .finally(() => (this.loading = false))
    },
    // months as text "11,12,1,2" in the form
    toModel(p) {
      return {
        ...p,
        open: false,
        components: (p.components || []).map((c) => ({
          ...c,
          rules: (c.rules || []).map((r) => ({ ...r, monthsText: (r.months || []).join(',') })),
        })),
      }
    },
    fromModel(p) {
      const { open, ...rest } = p
      return {
        ...rest,
        lat: p.lat === '' || p.lat === null ? null : Number(p.lat),
        lng: p.lng === '' || p.lng === null ? null : Number(p.lng),
        components: p.components.map((c) => ({
          name: c.name,
          price: Number(c.price) || 0,
          rules: c.rules.map(({ monthsText, ...r }) => ({
            ...r,
            price: Number(r.price) || 0,
            months: (monthsText || '')
              .split(/[ ,]+/)
              .filter(Boolean)
              .map(Number),
          })),
        })),
      }
    },
    addPlace() {
      this.places.push({
        id: '',
        name: this.places.length ? '' : 'Home',
        lat: null,
        lng: null,
        radiusM: 200,
        priceEntity: '',
        sessionFee: 0,
        lossPercent: 10,
        holidays: '',
        components: [{ name: 'Energy', price: 0, rules: [] }],
        open: true,
      })
    },
    addRule(c) {
      c.rules.push({ label: '', monthsText: '', days: 'all', from: '00:00', to: '00:00', price: 0 })
    },
    hereAsPosition(p) {
      navigator.geolocation &&
        navigator.geolocation.getCurrentPosition((pos) => {
          p.lat = +pos.coords.latitude.toFixed(6)
          p.lng = +pos.coords.longitude.toFixed(6)
        })
    },
    save(p) {
      this.saving = p.id || 'new'
      const body = this.fromModel(p)
      const req = p.id ? axios.put(`/api/chargingPlaces/${p.id}`, body) : axios.post('/api/chargingPlaces', body)
      req
        .then(({ data }) => {
          Object.assign(p, this.toModel(data), { open: true })
          this.$buefy.toast.open({ message: `${data.name} saved`, type: 'is-success', duration: 3000 })
        })
        .catch((ex) => {
          const msg = (ex.response && ex.response.data && ex.response.data.error) || ex.message
          this.$buefy.toast.open({ duration: 6000, message: msg, position: 'is-bottom', type: 'is-danger' })
        })
        .finally(() => (this.saving = ''))
    },
    remove(p, i) {
      if (!p.id) return this.places.splice(i, 1)
      this.$buefy.dialog.confirm({
        message: `Delete ${p.name}? Saved charges keep their price.`,
        type: 'is-danger',
        onConfirm: () => axios.delete(`/api/chargingPlaces/${p.id}`).then(() => this.places.splice(i, 1)),
      })
    },
  },
}
</script>

<template>
  <Layout>
    <h1 class="title">Charging places</h1>
    <p class="subtitle is-6"
      >Where electric cars charge and what a kWh costs there. The price is the sum of the parts; in each part the first rule
      that matches month, day and time wins, otherwise its default price. Times are local; "to" before "from" runs past
      midnight, 00:00–00:00 is all day.</p
    >
    <b-loading :active="loading" :is-full-page="false"></b-loading>
    <div v-for="(p, i) in places" :key="p.id || 'new' + i" class="box charging-place">
      <div class="is-flex is-justify-content-space-between is-align-items-center" @click="p.open = !p.open">
        <strong>{{ p.name || 'New place' }}</strong>
        <small class="has-text-grey"
          >{{ p.components.length }} part(s){{ p.priceEntity ? ' + ' + p.priceEntity : '' }}{{ p.lat !== null && p.lat !== '' ? ' · 📍' : '' }}</small
        >
      </div>
      <form v-if="p.open" class="mt-3" @submit.prevent="save(p)">
        <b-field grouped group-multiline>
          <b-field label="Name" expanded><b-input v-model="p.name" required maxlength="60" :has-counter="false"></b-input></b-field>
          <b-field label="Session fee"><b-input v-model.number="p.sessionFee" type="number" min="0" step=".01"></b-input></b-field>
          <b-field label="Charging loss %" message="Added to kWh you enter (what the car got)"
            ><b-input v-model.number="p.lossPercent" type="number" min="0" max="49" step=".5"></b-input
          ></b-field>
        </b-field>
        <b-field grouped group-multiline>
          <b-field label="Latitude"><b-input v-model="p.lat" type="number" step="any"></b-input></b-field>
          <b-field label="Longitude"><b-input v-model="p.lng" type="number" step="any"></b-input></b-field>
          <b-field label="Radius (m)"><b-input v-model.number="p.radiusM" type="number" min="10" step="10"></b-input></b-field>
          <b-field label="&nbsp;"><b-button icon-left="map-marker-alt" @click="hereAsPosition(p)">Here</b-button></b-field>
        </b-field>
        <b-field
          label="Home Assistant price sensor (optional)"
          message="Price per kWh over time (dynamic tariff, block tariff integration…); added to the parts below"
        >
          <b-input v-model.trim="p.priceEntity" placeholder="sensor.electricity_price" pattern="[a-z0-9_]+\.[a-z0-9_]+"></b-input>
        </b-field>
        <div v-for="(c, ci) in p.components" :key="ci" class="box p-3 tariff-component">
          <b-field grouped group-multiline>
            <b-field label="Part" expanded><b-input v-model="c.name" placeholder="Energy / Network"></b-input></b-field>
            <b-field label="Default price / kWh"><b-input v-model.number="c.price" type="number" step="any"></b-input></b-field>
            <b-field label="&nbsp;"><b-button type="is-danger" outlined icon-left="trash" @click="p.components.splice(ci, 1)"></b-button></b-field>
          </b-field>
          <div class="table-container">
            <table v-if="c.rules.length" class="table is-narrow is-fullwidth tariff-rules">
              <thead>
                <tr><th>Label</th><th>Months</th><th>Days</th><th>From</th><th>To</th><th>Price / kWh</th><th></th></tr>
              </thead>
              <tbody>
                <tr v-for="(r, ri) in c.rules" :key="ri">
                  <td><input v-model="r.label" class="input is-small" placeholder="Block 1" /></td>
                  <td><input v-model="r.monthsText" class="input is-small" placeholder="all, or 11,12,1,2" /></td>
                  <td>
                    <div class="select is-small">
                      <select v-model="r.days">
                        <option v-for="(t, v) in days" :key="v" :value="v">{{ t }}</option>
                      </select>
                    </div>
                  </td>
                  <td><input v-model="r.from" class="input is-small" type="time" required /></td>
                  <td><input v-model="r.to" class="input is-small" type="time" required /></td>
                  <td><input v-model.number="r.price" class="input is-small" type="number" step="any" /></td>
                  <td>
                    <a title="Up" :class="{ 'is-invisible': ri === 0 }" @click="c.rules.splice(ri - 1, 0, c.rules.splice(ri, 1)[0])">↑</a>
                    <a class="has-text-danger ml-2" title="Remove" @click="c.rules.splice(ri, 1)">✕</a>
                  </td>
                </tr>
              </tbody>
            </table>
          </div>
          <b-button size="is-small" icon-left="plus" class="add-rule" @click="addRule(c)">Rule</b-button>
        </div>
        <b-button size="is-small" icon-left="plus" class="add-component mb-3" @click="p.components.push({ name: '', price: 0, rules: [] })"
          >Price part</b-button
        >
        <b-field label="Holidays (count as free days)" message="One per line: MM-DD every year (12-25), or YYYY-MM-DD once (2027-03-29)">
          <b-input v-model="p.holidays" type="textarea" rows="3"></b-input>
        </b-field>
        <div class="buttons">
          <b-button native-type="submit" type="is-primary" :loading="saving === (p.id || 'new')">Save</b-button>
          <b-button type="is-danger" outlined @click="remove(p, i)">Delete</b-button>
        </div>
      </form>
    </div>
    <b-button icon-left="plus" class="add-place" @click="addPlace">Charging place</b-button>
  </Layout>
</template>
