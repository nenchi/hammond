<script>
import Layout from '@layouts/main.vue'
import { mapState } from 'vuex'
import store from '@state/store'
import axios from 'axios'

export default {
  components: { Layout },
  page: {
    title: 'Site Settings',
  },
  props: {
    user: {
      type: Object,
      required: true,
    },
    settings: {
      type: Object,
      required: true,
    },
  },
  data: function() {
    return {
      settingsModel: {
        currency: this.settings.currency,
        distanceUnit: this.settings.distanceUnit,
      },
      tryingToSave: false,
      // station price profiles, tried in order; keys are write-only (the server only says keySet)
      profiles: this.toModels(this.settings.fuelPriceProfiles),
      kinds: {
        tankerkoenig: { label: 'Germany – Tankerkönig', url: 'https://creativecommons.tankerkoenig.de/json', key: true },
        econtrol: { label: 'Austria – E-Control', url: 'https://api.e-control.at/sprit/1.0', key: false },
        custom: { label: 'Custom (Tankerkönig-compatible)', url: '', key: true },
      },
      testResult: null,
      testedProfile: '',
      haModel: { url: this.settings.haUrl || '', token: '' },
      evccModel: {
        url: this.settings.evccUrl || '',
        syncMinutes: this.settings.evccSyncMinutes || 0,
        placeId: this.settings.evccPlaceId || '',
        since: this.settings.evccSince ? this.settings.evccSince.slice(0, 10) : '',
      },
      chargingPlaces: [],
      evccResult: null,
      syncing: false,
      haTokenSet: !!this.settings.haTokenSet,
      testing: false,
    }
  },
  computed: {
    ...mapState('vehicles', ['currencyMasters', 'distanceUnitMasters']),
  },
  mounted() {
    axios.get('/api/chargingPlaces').then(({ data }) => (this.chargingPlaces = data))
  },
  methods: {
    toModels(list) {
      return (list || []).map((p) => ({ id: p.id, name: p.name, kind: p.kind, url: p.url || '', apiKey: '', keySet: p.keySet }))
    },
    addProfile(kind) {
      const names = { tankerkoenig: 'Germany – Tankerkönig', econtrol: 'Austria – E-Control', custom: 'Custom' }
      this.profiles.push({ id: '', name: names[kind], kind, url: '', apiKey: '', keySet: false })
    },
    move(i, d) {
      const p = this.profiles.splice(i, 1)[0]
      this.profiles.splice(i + d, 0, p)
    },
    errorText(ex) {
      return (ex.response && ex.response.data && (ex.response.data.error || JSON.stringify(ex.response.data.errors))) || ex.message
    },
    // asks one saved profile with its saved key (a fixed point in its country)
    testPrices(p) {
      this.testing = true
      this.testResult = null
      this.testedProfile = p.id
      axios
        .get('/api/settings/fuelPrices/test', { params: { profile: p.id } })
        .then(({ data }) => (this.testResult = data))
        .catch((ex) => (this.testResult = { ok: false, error: ex.message }))
        .finally(() => (this.testing = false))
    },
    savePriceSettings() {
      const profiles = this.profiles.map(({ id, name, kind, url, apiKey }) => ({ id, name, kind, url, apiKey }))
      axios
        .post('/api/settings/fuelPrices', { profiles })
        .then(({ data }) => {
          this.profiles = this.toModels(data.fuelPriceProfiles)
          store.dispatch('utils/getSettings')
          this.$buefy.toast.open({ message: 'Fuel price settings saved', type: 'is-success', duration: 3000 })
        })
        .catch((ex) => this.$buefy.toast.open({ duration: 5000, message: this.errorText(ex), position: 'is-bottom', type: 'is-danger' }))
    },
    saveEvcc() {
      const m = this.evccModel
      axios
        .post('/api/settings/evcc', { ...m, since: m.since ? new Date(m.since + 'T00:00:00').toISOString() : null })
        .then(({ data }) => {
          this.evccModel.since = data.evccSince ? data.evccSince.slice(0, 10) : ''
          store.dispatch('utils/getSettings')
          this.$buefy.toast.open({ message: 'EVCC settings saved', type: 'is-success', duration: 3000 })
        })
        .catch((ex) => this.$buefy.toast.open({ duration: 5000, message: this.errorText(ex), position: 'is-bottom', type: 'is-danger' }))
    },
    syncEvcc() {
      this.syncing = true
      this.evccResult = null
      axios
        .post('/api/settings/evcc/sync')
        .then(({ data }) => {
          this.evccResult = { ok: true, ...data }
          store.dispatch('vehicles/fetchDetections').catch(() => {})
        })
        .catch((ex) => (this.evccResult = { ok: false, error: this.errorText(ex) }))
        .finally(() => (this.syncing = false))
    },
    saveHomeAssistant() {
      axios
        .post('/api/settings/homeAssistant', this.haModel)
        .then(({ data }) => {
          this.haTokenSet = data.haTokenSet
          this.haModel = { url: data.haUrl || '', token: '' }
          store.dispatch('utils/getSettings')
          this.$buefy.toast.open({ message: 'Home Assistant settings saved', type: 'is-success', duration: 3000 })
        })
        .catch((ex) => this.$buefy.toast.open({ duration: 5000, message: this.errorText(ex), position: 'is-bottom', type: 'is-danger' }))
    },
    saveSettings() {
      this.tryingToSave = true
      store
        .dispatch(`utils/saveSettings`, { settings: this.settingsModel })
        .then((data) => {
          this.$buefy.toast.open({
            message: 'Settings saved successfully',
            type: 'is-success',
            duration: 3000,
          })
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
          this.tryingToSave = false
        })
    },
  },
}
</script>

<template>
  <Layout>
    <div class="">
      <div class="columns">
        <div class="column">
          <h1 class="title">Site Settings</h1>
          <h1 class="subtitle">
            Update site level settings. These will be used as default values for new users.
          </h1>
        </div>
      </div>
    </div>
    <br />
    <form class="" @submit.prevent="saveSettings">
      <b-field label="Currency">
        <b-select v-model="settingsModel.currency" placeholder="Currency" required expanded>
          <option v-for="option in currencyMasters" :key="option.code" :value="option.code">
            {{ `${option.namePlural} (${option.code})` }}
          </option>
        </b-select>
      </b-field>
      <b-field label="Distance Unit">
        <b-select v-model.number="settingsModel.distanceUnit" placeholder="Distance Unit" required expanded>
          <option v-for="(option, key) in distanceUnitMasters" :key="key" :value="key">
            {{ `${option.long} (${option.short})` }}
          </option>
        </b-select>
      </b-field>
      <br />
      <b-field>
        <b-button tag="input" native-type="submit" :disabled="tryingToSave" type="is-primary" value="Save" expanded> </b-button>
      </b-field>
    </form>
    <br />
    <form class="box fuel-prices" @submit.prevent="savePriceSettings">
      <h1 class="subtitle">Fuel prices</h1>
      <p class="mb-3"
        >Shows stations nearby with their current price in the fill-up form. Profiles are asked in this order: the first
        one with stations near you is used (each service only knows its own country), the form can switch to another.
        Germany needs a free key from
        <a href="https://creativecommons.tankerkoenig.de" target="_blank" rel="noopener">creativecommons.tankerkoenig.de</a>;
        Austria needs none. "Custom" works with any service answering in the Tankerkönig format (e.g. your own proxy).</p
      >
      <div v-for="(p, i) in profiles" :key="p.id || 'new' + i" class="box price-profile p-3">
        <b-field grouped group-multiline>
          <b-field label="Name" expanded>
            <b-input v-model="p.name" maxlength="40" :has-counter="false" required></b-input>
          </b-field>
          <b-field label="Type">
            <b-select v-model="p.kind">
              <option v-for="(k, value) in kinds" :key="value" :value="value">{{ k.label }}</option>
            </b-select>
          </b-field>
        </b-field>
        <b-field label="URL" :message="kinds[p.kind].url ? 'Empty = ' + kinds[p.kind].url : ''">
          <b-input v-model="p.url" type="url" :placeholder="kinds[p.kind].url || 'https://…/json'" :required="p.kind === 'custom'"></b-input>
        </b-field>
        <b-field v-if="kinds[p.kind].key" label="API key" :message="p.keySet ? 'A key is set; leave empty to keep it, enter - to remove it.' : ''">
          <b-input v-model="p.apiKey" type="password" password-reveal :placeholder="p.keySet ? '••••••••' : ''"></b-input>
        </b-field>
        <div class="buttons mb-0">
          <b-button size="is-small" icon-left="arrow-up" :disabled="i === 0" title="Ask earlier" @click="move(i, -1)"></b-button>
          <b-button size="is-small" icon-left="arrow-down" :disabled="i === profiles.length - 1" title="Ask later" @click="move(i, 1)"></b-button>
          <b-button size="is-small" class="test-prices" :loading="testing && testedProfile === p.id" :disabled="!p.id" @click="testPrices(p)"
            >Test connection</b-button
          >
          <b-button size="is-small" type="is-danger" outlined icon-left="trash" @click="profiles.splice(i, 1)">Remove</b-button>
        </div>
        <b-notification
          v-if="testResult && testedProfile === p.id"
          :type="testResult.ok ? 'is-success' : 'is-danger'"
          :closable="false"
          class="test-result mt-2"
        >
          <template v-if="testResult.ok">
            ✓ Works: {{ testResult.stations }} stations found<span v-if="testResult.sample && testResult.sample[0]"
              >, e.g. {{ testResult.sample[0].brand || testResult.sample[0].name }}:
              {{ Object.entries(testResult.sample[0].prices).map(([g, v]) => g.toUpperCase() + ' ' + v.toFixed(3)).join(' · ') }}</span
            >. <small>{{ testResult.attribution }}</small>
          </template>
          <template v-else>✗ {{ testResult.error }}</template>
        </b-notification>
      </div>
      <div class="buttons">
        <b-button v-for="(k, value) in kinds" :key="value" size="is-small" icon-left="plus" class="add-profile" @click="addProfile(value)">{{
          k.label
        }}</b-button>
      </div>
      <b-button tag="input" native-type="submit" type="is-primary" value="Save fuel prices" expanded></b-button>
      <p class="help">Test uses the saved settings: save first.</p>
    </form>
    <form class="box home-assistant" @submit.prevent="saveHomeAssistant">
      <h1 class="subtitle">Home Assistant</h1>
      <p class="mb-3"
        >Pre-fills a new fill-up with the car's current odometer. Set the odometer entity per vehicle (Edit vehicle). Token:
        Home Assistant → your profile → Security → Long-lived access tokens.</p
      >
      <b-field label="URL">
        <b-input v-model="haModel.url" type="url" placeholder="http://homeassistant.local:8123"></b-input>
      </b-field>
      <b-field label="Access token" :message="haTokenSet ? 'A token is set; leave empty to keep it, enter - to remove it.' : ''">
        <b-input v-model="haModel.token" type="password" password-reveal :placeholder="haTokenSet ? '••••••••' : ''"></b-input>
      </b-field>
      <b-button tag="input" native-type="submit" type="is-primary" value="Save Home Assistant" expanded></b-button>
    </form>
    <form class="box evcc" @submit.prevent="saveEvcc">
      <h1 class="subtitle">EVCC</h1>
      <p class="mb-3"
        >Imports finished charging sessions from EVCC as detections to confirm (bell icon), priced with the charging place's
        tariff (else EVCC's own price). Link a vehicle in Edit vehicle → "EVCC vehicle". Prices:
        <router-link to="/admin/charging">Charging places</router-link>.</p
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
        <b-button class="sync-evcc" :loading="syncing" :disabled="!settings.evccUrl && !evccModel.url" @click="syncEvcc">Sync now</b-button>
      </div>
      <b-notification v-if="evccResult" :type="evccResult.ok ? 'is-success' : 'is-danger'" :closable="false" class="evcc-result">
        <template v-if="evccResult.ok"
          >✓ {{ evccResult.imported }} imported, {{ evccResult.skipped }} skipped (other vehicles, older, unfinished or already
          imported).<span v-for="n in evccResult.notes" :key="n"><br />{{ n }}</span></template
        >
        <template v-else>✗ {{ evccResult.error }}</template>
      </b-notification>
    </form>
  </Layout>
</template>
