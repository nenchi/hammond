<script>
import axios from 'axios'

// Station price profiles, tried in order; keys are write-only (the server only says keySet). Used for the
// site's profiles (admin: share them or not) and each user's own ones.
export default {
  props: {
    initial: { type: Array, default: () => [] },
    saveUrl: { type: String, required: true },
    title: { type: String, default: 'Fuel prices' },
    shareable: { type: Boolean, default: false }, // site profiles: "share with users"
  },
  data() {
    return {
      profiles: this.toModels(this.initial),
      kinds: {
        tankerkoenig: { label: 'Germany – Tankerkönig', url: 'https://creativecommons.tankerkoenig.de/json', key: true },
        econtrol: { label: 'Austria – E-Control', url: 'https://api.e-control.at/sprit/1.0', key: false },
        custom: { label: 'Custom (Tankerkönig-compatible)', url: '', key: true },
      },
      testResult: null,
      testedProfile: '',
      testing: false,
    }
  },
  watch: {
    initial(list) {
      this.profiles = this.toModels(list)
    },
  },
  methods: {
    toModels(list) {
      return (list || []).map((p) => ({ id: p.id, name: p.name, kind: p.kind, url: p.url || '', apiKey: '', keySet: p.keySet, shared: !p.private }))
    },
    addProfile(kind) {
      const names = { tankerkoenig: 'Germany – Tankerkönig', econtrol: 'Austria – E-Control', custom: 'Custom' }
      this.profiles.push({ id: '', name: names[kind], kind, url: '', apiKey: '', keySet: false, shared: true })
    },
    move(i, d) {
      const p = this.profiles.splice(i, 1)[0]
      this.profiles.splice(i + d, 0, p)
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
      const profiles = this.profiles.map(({ id, name, kind, url, apiKey, shared }) => ({ id, name, kind, url, apiKey, private: !shared }))
      axios
        .post(this.saveUrl, { profiles })
        .then(({ data }) => {
          const list = Array.isArray(data) ? data : data.fuelPriceProfiles
          this.profiles = this.toModels(list)
          this.$emit('saved', list)
          this.$buefy.toast.open({ message: 'Fuel price settings saved', type: 'is-success', duration: 3000 })
        })
        .catch((ex) => {
          const msg = (ex.response && ex.response.data && (ex.response.data.error || JSON.stringify(ex.response.data.errors))) || ex.message
          this.$buefy.toast.open({ duration: 5000, message: msg, position: 'is-bottom', type: 'is-danger' })
        })
    },
  },
}
</script>

<template>
  <form class="box fuel-prices" @submit.prevent="savePriceSettings">
    <h1 class="subtitle">{{ title }}</h1>
    <p class="mb-3"
      >Shows stations nearby with their current price in the fill-up form. Profiles are asked in this order: the first
      one with stations near you is used (each service only knows its own country), the form can switch to another.
      Germany needs a free key from
      <a href="https://creativecommons.tankerkoenig.de" target="_blank" rel="noopener">creativecommons.tankerkoenig.de</a>;
      Austria needs none. "Custom" works with any service answering in the Tankerkönig format (e.g. your own proxy).
      <slot></slot></p
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
      <b-field v-if="kinds[p.kind].key" label="API key" :message="p.keySet ? 'A key is saved. hammond never shows it again (the eye only shows what you type now); leave empty to keep it, - removes it.' : ''">
        <b-input v-model="p.apiKey" type="password" password-reveal :placeholder="p.keySet ? 'saved, hidden – type a new one to replace it' : ''"></b-input>
      </b-field>
      <b-field v-if="shareable">
        <b-checkbox v-model="p.shared" class="share-profile">Share with users (they can use it, not see the key)</b-checkbox>
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
</template>
