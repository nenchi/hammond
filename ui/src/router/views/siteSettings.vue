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
      // station prices; the key is write-only (the server only says whether one is set)
      priceModel: {
        provider: this.settings.fuelPriceProvider || '',
        url: this.settings.fuelPriceUrl || '',
        apiKey: '',
      },
      priceKeySet: !!this.settings.fuelPriceKeySet,
      tankerkoenigUrl: 'https://creativecommons.tankerkoenig.de/json',
      testResult: null,
      savedProvider: this.settings.fuelPriceProvider || '',
      testing: false,
    }
  },
  computed: {
    ...mapState('vehicles', ['currencyMasters', 'distanceUnitMasters']),
  },
  methods: {
    // asks the saved provider with the saved key (Frankfurt city centre)
    testPrices() {
      this.testing = true
      this.testResult = null
      axios
        .get('/api/settings/fuelPrices/test')
        .then(({ data }) => (this.testResult = data))
        .catch((ex) => (this.testResult = { ok: false, error: ex.message }))
        .finally(() => (this.testing = false))
    },
    savePriceSettings() {
      axios
        .post('/api/settings/fuelPrices', this.priceModel)
        .then(({ data }) => {
          this.priceKeySet = data.fuelPriceKeySet
          this.savedProvider = data.fuelPriceProvider || ''
          this.priceModel.apiKey = ''
          store.dispatch('utils/getSettings')
          this.$buefy.toast.open({ message: 'Fuel price settings saved', type: 'is-success', duration: 3000 })
        })
        .catch((ex) => {
          const msg = (ex.response && ex.response.data && (ex.response.data.error || JSON.stringify(ex.response.data.errors))) || ex.message
          this.$buefy.toast.open({ duration: 5000, message: msg, position: 'is-bottom', type: 'is-danger' })
        })
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
        >Shows stations nearby with their current price in the fill-up form. Germany: get a free key at
        <a href="https://creativecommons.tankerkoenig.de" target="_blank" rel="noopener">creativecommons.tankerkoenig.de</a>.
        "Custom" works with any service that answers in the same format (e.g. your own proxy).</p
      >
      <b-field label="Provider">
        <b-select v-model="priceModel.provider" expanded>
          <option value="">Off</option>
          <option value="tankerkoenig">Germany – Tankerkönig</option>
          <option value="custom">Custom (Tankerkönig-compatible)</option>
        </b-select>
      </b-field>
      <template v-if="priceModel.provider">
        <b-field label="URL" :message="priceModel.provider === 'tankerkoenig' ? 'Empty = ' + tankerkoenigUrl : ''">
          <b-input
            v-model="priceModel.url"
            type="url"
            :placeholder="priceModel.provider === 'tankerkoenig' ? tankerkoenigUrl : 'https://…/json'"
            :required="priceModel.provider === 'custom'"
          ></b-input>
        </b-field>
        <b-field label="API key" :message="priceKeySet ? 'A key is set; leave empty to keep it, enter - to remove it.' : ''">
          <b-input v-model="priceModel.apiKey" type="password" password-reveal :placeholder="priceKeySet ? '••••••••' : ''"></b-input>
        </b-field>
      </template>
      <b-field grouped>
        <p class="control is-expanded">
          <b-button tag="input" native-type="submit" type="is-primary" value="Save fuel prices" expanded></b-button>
        </p>
        <p class="control">
          <b-button class="test-prices" :loading="testing" :disabled="!savedProvider" @click="testPrices"
            >Test connection</b-button
          >
        </p>
      </b-field>
      <p class="help">Test uses the saved settings: save first.</p>
      <b-notification v-if="testResult" :type="testResult.ok ? 'is-success' : 'is-danger'" :closable="false" class="test-result mt-2">
        <template v-if="testResult.ok">
          ✓ Works: {{ testResult.stations }} stations found near Frankfurt<span v-if="testResult.sample && testResult.sample[0]"
            >, e.g. {{ testResult.sample[0].brand || testResult.sample[0].name }}:
            {{ Object.entries(testResult.sample[0].prices).map(([g, p]) => g.toUpperCase() + ' ' + p.toFixed(3)).join(' · ') }}</span
          >. <small>{{ testResult.attribution }}</small>
        </template>
        <template v-else>✗ {{ testResult.error }}</template>
      </b-notification>
    </form>
  </Layout>
</template>
