<script>
import Layout from '@layouts/main.vue'
import { parseAndFormatDateTime, parseAndFormatDate } from '@utils/format-date'
import { mapState } from 'vuex'
import axios from 'axios'
import store from '@state/store'

export default {
  page: {
    title: 'Detected Fill-ups',
  },
  components: { Layout },
  computed: {
    ...mapState('vehicles', ['detections', 'vehicles']),
    ...mapState('users', ['me']),
  },
  mounted() {
    store.dispatch('vehicles/fetchDetections')
  },
  methods: {
    parseAndFormatDateTime,
    parseAndFormatDate,
    vehicleName(id) {
      const v = (this.vehicles || []).find((x) => x.id === id)
      return v ? `${v.nickname} - ${v.registration}` : ''
    },
    unit(id) {
      const v = (this.vehicles || []).find((x) => x.id === id)
      return v && v.fuelUnitDetail ? v.fuelUnitDetail.short : ''
    },
    act(d, action, message) {
      axios
        .post(`/api/detections/${d.id}/${action}`)
        .then(() => {
          this.$buefy.toast.open({ message, type: 'is-success', duration: 3000 })
          store.dispatch('vehicles/fetchDetections')
        })
        .catch((ex) => {
          this.$buefy.toast.open({ duration: 5000, message: ex.message, position: 'is-bottom', type: 'is-danger' })
        })
    },
  },
}
</script>

<template>
  <Layout>
    <h1 class="title">Detected Fill-ups</h1>
    <p class="subtitle is-6"
      >Suggested by Home Assistant or another system through the API. They don't count until you complete them.</p
    >
    <div v-if="!detections.length" class="box">
      Nothing detected right now. To get fill-ups suggested automatically, create an API key in
      <router-link to="/settings">Settings</router-link> and set up the
      <a href="https://github.com/nenchi/hammond#automatic-fill-up-detection-home-assistant--api" target="_blank" rel="noopener"
        >Home Assistant blueprint</a
      >.
    </div>
    <div class="columns is-multiline">
      <div v-for="d in detections" :key="d.id" class="column is-6">
        <div class="card detection" :data-detection="d.id">
          <div class="card-header">
            <div class="card-header-title">{{ vehicleName(d.vehicleId) }}</div>
            <div class="card-header-icon">
              <b-tag v-if="d.duplicateOf" type="is-warning">possible duplicate</b-tag>
              <b-tag v-else-if="d.uncertain" type="is-warning">check quantity</b-tag>
            </div>
          </div>
          <div class="card-content">
            <div class="columns is-mobile">
              <div class="column">
                <p class="heading">Detected · {{ d.source }}</p>
                <p>{{ parseAndFormatDateTime(d.date) }}</p>
                <p
                  ><strong>{{ d.estimated ? '~' : '' }}{{ d.fuelQuantity }} {{ unit(d.vehicleId) }}</strong>
                  <span v-if="d.levelBefore !== null && d.levelAfter !== null"> ({{ d.levelBefore }}% → {{ d.levelAfter }}%)</span></p
                >
                <p>{{ d.odoReading }} {{ me.distanceUnitDetail.short }}</p>
                <p>{{ d.isTankFull ? 'Full tank' : 'Partial' }}</p>
              </div>
              <div v-if="d.duplicateOf" class="column">
                <p class="heading">Your entry</p>
                <p>{{ parseAndFormatDate(d.duplicateOf.date) }}</p>
                <p
                  ><strong>{{ d.duplicateOf.fuelQuantity }} {{ unit(d.vehicleId) }}</strong> · {{ d.duplicateOf.totalAmount }}
                  {{ me.currency }}</p
                >
                <p>{{ d.duplicateOf.odoReading }} {{ me.distanceUnitDetail.short }}</p>
              </div>
            </div>
            <p v-if="d.uncertain" class="help is-warning">Estimate may be low: {{ d.uncertainNote }}.</p>
          </div>
          <footer class="card-footer">
            <template v-if="d.duplicateOf">
              <a class="card-footer-item" @click="act(d, 'discard', 'Kept your entry')">Keep mine</a>
              <a class="card-footer-item" @click="act(d, 'apply', 'Detected values applied to your entry')">Use detected values</a>
            </template>
            <template v-else>
              <router-link class="card-footer-item" :to="`/vehicles/${d.vehicleId}/fillup?detection=${d.id}`"
                >Complete</router-link
              >
              <a class="card-footer-item" @click="act(d, 'discard', 'Detection discarded')">Discard</a>
            </template>
          </footer>
        </div>
      </div>
    </div>
  </Layout>
</template>
