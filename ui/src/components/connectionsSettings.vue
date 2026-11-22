<script>
import axios from 'axios'
import store from '@state/store'

// The user's own connections: Home Assistant (URL + write-only token), MQTT (dashboard values to HA) and EVCC.
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
      mqttModel: { url: this.me.mqttUrl || '', user: this.me.mqttUser || '', password: '', prefix: this.me.mqttPrefix || '' },
      mqttPasswordSet: !!this.me.mqttPasswordSet,
      savedMqttUrl: this.me.mqttUrl || '',
      mqttResult: null,
      publishing: false,
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
    saveMqtt() {
      axios
        .post('/api/me/mqtt', this.mqttModel)
        .then(({ data }) => {
          this.mqttPasswordSet = data.mqttPasswordSet
          this.savedMqttUrl = data.mqttUrl || ''
          this.mqttModel.password = ''
          store.dispatch('users/me').catch(() => {})
          this.toast(data.mqttUrl ? 'MQTT settings saved' : 'MQTT turned off')
          if (data.mqttUrl) this.publishMqtt()
        })
        .catch((ex) => this.toast(this.errorText(ex), false))
    },
    publishMqtt() {
      this.publishing = true
      this.mqttResult = null
      axios
        .post('/api/me/mqtt/publish')
        .then(({ data }) => (this.mqttResult = { ok: true, ...data }))
        .catch((ex) => (this.mqttResult = { ok: false, error: this.errorText(ex) }))
        .finally(() => (this.publishing = false))
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
      <b-field label="Access token" :message="haTokenSet ? 'A token is saved. hammond never shows it again (the eye only shows what you type now); leave empty to keep it, - removes it. Test connection checks it.' : ''">
        <b-input v-model="haModel.token" type="password" password-reveal :placeholder="haTokenSet ? 'saved, hidden – type a new one to replace it' : ''"></b-input>
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
    <form class="box mqtt" @submit.prevent="saveMqtt">
      <h1 class="subtitle">MQTT (Home Assistant dashboard)</h1>
      <p class="mb-3"
        >hammond sends your numbers to Home Assistant through your MQTT broker. Home Assistant creates the devices by itself (MQTT
        discovery): one per vehicle (costs this month, last fill-up, consumption, odometer, trips) and one per watched station
        (price, limit, below limit). Updated after every change and every 5 minutes. Empty URL = off.</p
      >
      <b-field label="Broker URL"><b-input v-model.trim="mqttModel.url" placeholder="mqtt://192.168.1.10:1883"></b-input></b-field>
      <b-field grouped group-multiline>
        <b-field label="Username" expanded><b-input v-model.trim="mqttModel.user" autocomplete="off"></b-input></b-field>
        <b-field label="Password" expanded :message="mqttPasswordSet ? 'saved, hidden – leave empty to keep, - removes it' : ''">
          <b-input v-model="mqttModel.password" type="password" password-reveal autocomplete="new-password"></b-input>
        </b-field>
        <b-field label="Discovery prefix" message="Empty = homeassistant">
          <b-input v-model.trim="mqttModel.prefix" placeholder="homeassistant"></b-input>
        </b-field>
      </b-field>
      <div class="buttons">
        <b-button native-type="submit" type="is-primary">Save MQTT</b-button>
        <b-button class="publish-mqtt" :loading="publishing" :disabled="!savedMqttUrl" @click="publishMqtt">Send now</b-button>
      </div>
      <b-notification v-if="mqttResult" :type="mqttResult.ok ? 'is-success' : 'is-danger'" :closable="false" class="mqtt-result">
        <template v-if="mqttResult.ok"
          >✓ Sent: {{ mqttResult.vehicles }} vehicle(s), {{ mqttResult.watches }} watched station(s)<span v-if="mqttResult.removed">,
            {{ mqttResult.removed }} old one(s) removed</span
          >. In Home Assistant: Settings → Devices → MQTT, devices starting with "hammond".</template
        >
        <template v-else>✗ {{ mqttResult.error }}</template>
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
