<script>
import axios from 'axios'
import store from '@state/store'

// The user's own connections: Home Assistant (URL + write-only token) and EVCC.
export default {
  props: {
    me: { type: Object, required: true },
  },
  data() {
    return {
      haModel: { url: this.me.haUrl || '', token: '' },
      haTokenSet: !!this.me.haTokenSet,
      haTest: null,
      haTesting: false,
      evccModel: {
        url: this.me.evccUrl || '',
        syncMinutes: this.me.evccSyncMinutes || 0,
        placeId: this.me.evccPlaceId || '',
        since: this.me.evccSince ? this.me.evccSince.slice(0, 10) : '',
      },
      savedEvccUrl: this.me.evccUrl || '',
      chargingPlaces: [],
      evccResult: null,
      syncing: false,
    }
  },
  mounted() {
    axios.get('/api/chargingPlaces').then(({ data }) => (this.chargingPlaces = data))
  },
  methods: {
    errorText(ex) {
      return (ex.response && ex.response.data && (ex.response.data.error || JSON.stringify(ex.response.data.errors))) || ex.message
    },
    toast(message, ok = true) {
      this.$buefy.toast.open(ok ? { message, type: 'is-success', duration: 3000 } : { message, duration: 5000, position: 'is-bottom', type: 'is-danger' })
    },
    // asks Home Assistant with the saved URL + token (and reads your first vehicle's odometer)
    testHomeAssistant() {
      this.haTesting = true
      this.haTest = null
      axios
        .get('/api/me/homeAssistant/test')
        .then(({ data }) => (this.haTest = data))
        .catch((ex) => (this.haTest = { ok: false, error: this.errorText(ex) }))
        .finally(() => (this.haTesting = false))
    },
    saveHomeAssistant() {
      axios
        .post('/api/me/homeAssistant', this.haModel)
        .then(({ data }) => {
          this.haTokenSet = data.haTokenSet
          this.haModel = { url: data.haUrl || '', token: '' }
          store.dispatch('users/me').catch(() => {})
          this.toast('Home Assistant settings saved')
        })
        .catch((ex) => this.toast(this.errorText(ex), false))
    },
    saveEvcc() {
      const m = this.evccModel
      axios
        .post('/api/me/evcc', { ...m, since: m.since ? new Date(m.since + 'T00:00:00').toISOString() : null })
        .then(({ data }) => {
          this.evccModel.since = data.evccSince ? data.evccSince.slice(0, 10) : ''
          this.savedEvccUrl = data.evccUrl || ''
          store.dispatch('users/me').catch(() => {})
          this.toast('EVCC settings saved')
        })
        .catch((ex) => this.toast(this.errorText(ex), false))
    },
    syncEvcc() {
      this.syncing = true
      this.evccResult = null
      axios
        .post('/api/me/evcc/sync')
        .then(({ data }) => {
          this.evccResult = { ok: true, ...data }
          store.dispatch('vehicles/fetchDetections').catch(() => {})
        })
        .catch((ex) => (this.evccResult = { ok: false, error: this.errorText(ex) }))
        .finally(() => (this.syncing = false))
    },
  },
}
</script>

<template>
  <div>
    <form class="box home-assistant" @submit.prevent="saveHomeAssistant">
      <h1 class="subtitle">Home Assistant</h1>
      <p class="mb-3"
        >Your own Home Assistant: pre-fills a new fill-up with the car's current odometer (set the odometer entity in Edit
        vehicle) and reads the price sensors of your charging places. Token: Home Assistant → your profile → Security →
        Long-lived access tokens. (The hammond API keys below are for the other direction: HA → hammond.)</p
      >
      <b-field label="URL">
        <b-input v-model="haModel.url" type="url" placeholder="http://homeassistant.local:8123"></b-input>
      </b-field>
      <b-field label="Access token" :message="haTokenSet ? 'A token is set; leave empty to keep it, enter - to remove it.' : ''">
        <b-input v-model="haModel.token" type="password" password-reveal :placeholder="haTokenSet ? '••••••••' : ''"></b-input>
      </b-field>
      <div class="buttons">
        <b-button native-type="submit" type="is-primary">Save Home Assistant</b-button>
        <b-button class="test-ha" :loading="haTesting" :disabled="!haTokenSet" @click="testHomeAssistant">Test connection</b-button>
      </div>
      <p class="help">Test uses the saved URL and token: save first.</p>
      <b-notification v-if="haTest" :type="haTest.ok ? 'is-success' : 'is-danger'" :closable="false" class="ha-test-result mt-2">
        <template v-if="haTest.ok"
          >✓ Connected to {{ haTest.name || 'Home Assistant' }} ({{ haTest.version }})<span v-if="haTest.odometer"
            ><br />Odometer {{ haTest.odometer }}</span
          ></template
        >
        <template v-else>✗ {{ haTest.error }}</template>
      </b-notification>
    </form>
    <form class="box evcc" @submit.prevent="saveEvcc">
      <h1 class="subtitle">EVCC</h1>
      <p class="mb-3"
        >Your own EVCC: imports finished charging sessions from EVCC as detections to confirm (bell icon), priced with the charging place's
        tariff (else EVCC's own price). Link a vehicle in Edit vehicle → "EVCC vehicle". Prices:
        <router-link to="/charging">Charging places</router-link>.</p
      >
      <b-field label="URL"><b-input v-model="evccModel.url" type="url" placeholder="http://evcc.local:7070"></b-input></b-field>
      <b-field grouped group-multiline>
        <b-field label="Sync every (minutes)" message="0 = only Sync now">
          <b-input v-model.number="evccModel.syncMinutes" type="number" min="0" max="1440"></b-input>
        </b-field>
        <b-field label="Charging place of EVCC" expanded>
          <b-select v-model="evccModel.placeId" expanded>
            <option value="">— EVCC's price —</option>
            <option v-for="p in chargingPlaces" :key="p.id" :value="p.id">{{ p.name }}</option>
          </b-select>
        </b-field>
        <b-field label="Import sessions from" message="Empty at the first save = the last 7 days">
          <b-input v-model="evccModel.since" type="date"></b-input>
        </b-field>
      </b-field>
      <div class="buttons">
        <b-button native-type="submit" type="is-primary">Save EVCC</b-button>
        <b-button class="sync-evcc" :loading="syncing" :disabled="!savedEvccUrl" @click="syncEvcc">Sync now</b-button>
      </div>
      <b-notification v-if="evccResult" :type="evccResult.ok ? 'is-success' : 'is-danger'" :closable="false" class="evcc-result">
        <template v-if="evccResult.ok"
          >✓ {{ evccResult.imported }} imported, {{ evccResult.skipped }} skipped (other vehicles, older, unfinished or already
          imported).<span v-for="n in evccResult.notes" :key="n"><br />{{ n }}</span></template
        >
        <template v-else>✗ {{ evccResult.error }}</template>
      </b-notification>
    </form>
  </div>
</template>
