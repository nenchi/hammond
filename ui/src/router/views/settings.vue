<script>
import Layout from '@layouts/main.vue'
import { mapState } from 'vuex'
import store from '@state/store'
import axios from 'axios'
import { parseAndFormatDateTime } from '@utils/format-date'

export default {
  page: {
    title: 'Settings',
  },
  components: { Layout },
  props: {
    user: {
      type: Object,
      required: true,
    },
    me: {
      type: Object,
      required: true,
    },
  },
  data: function() {
    return {
      settingsModel: {
        currency: this.me.currency,
        distanceUnit: this.me.distanceUnit,
        dateFormat: this.me.dateFormat,
        theme: this.me.theme || 'auto',
        prefillFillup: this.me.prefillFillup !== false,
        detectionDuplicateMode: this.me.detectionDuplicateMode || 'ask',
        showFillupTime: this.me.showFillupTime !== false,
        stationSuggestions: this.me.stationSuggestions || 'location',
      },
      apiKeys: [],
      newKeyName: 'Home Assistant',
      createdKey: null,
      // set at build time from the VERSION file (Dockerfile)
      appVersion: process.env.VUE_APP_VERSION || 'dev',
      repo: 'https://github.com/nenchi/hammond',
      tryingToSave: false,
      changePassModel: {
        old: '',
        new: '',
        renew: '',
      },
      dateFormatMasters: ['dd/MM/yyyy', 'MM/dd/yyyy', 'yyyy/MM/dd'],
    }
  },
  mounted() {
    this.fetchApiKeys()
  },
  computed: {
    ...mapState('vehicles', ['currencyMasters', 'distanceUnitMasters']),
    ...mapState('auth', ['currentUser']),
    isAdmin() {
      return this.currentUser && this.currentUser.role === 'ADMIN'
    },
    passwordValid() {
      if (this.changePassModel.new === '' || this.changePassModel.renew === '') {
        return true
      }

      return this.changePassModel.new === this.changePassModel.renew
    },
    filteredCurrencyMasters() {
      return this.currencyMasters.filter((option) => {
        return (
          option.namePlural
            .toString()
            .toLowerCase()
            .indexOf(this.settingsModel.currency.toLowerCase()) >= 0 ||
          option.code
            .toString()
            .toLowerCase()
            .indexOf(this.settingsModel.currency.toLowerCase()) >= 0
        )
      })
    },
  },
  methods: {
    parseAndFormatDateTime,
    changePassword() {
      if (!this.passwordValid) {
        return
      }
      this.tryingToSavePass = true

      axios
        .post('/api/changePassword', {
          oldPassword: this.changePassModel.old,
          newPassword: this.changePassModel.new,
        })
        .then((data) => {
          this.$buefy.toast.open({
            message: 'Password changed successfully. You will be logged out now.',
            type: 'is-success',
            duration: 3000,
          })
          this.$router
            .push({ name: 'logout' })
            .then((succ) => {
              console.log(succ)
            })
            .catch((err) => console.log('error:', err))
        })
        .catch((ex) => {
          let errorMessage = ex.message
          if (ex.response && ex.response.data?.errors?.changePassword) {
            errorMessage = ex.response.data?.errors?.changePassword
          }
          this.$buefy.toast.open({
            duration: 5000,
            message: errorMessage,
            position: 'is-bottom',
            type: 'is-danger',
          })
        })
        .finally(() => {
          this.tryingToSavePass = false
        })
    },
    saveSettings() {
      this.tryingToSave = true
      store
        .dispatch(`utils/saveUserSettings`, { settings: this.settingsModel })
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
    fetchApiKeys() {
      axios.get('/api/me/apikeys').then(({ data }) => (this.apiKeys = data || []))
    },
    createApiKey() {
      axios
        .post('/api/me/apikeys', { name: this.newKeyName })
        .then(({ data }) => {
          this.createdKey = data.key
          this.fetchApiKeys()
        })
        .catch((ex) => this.$buefy.toast.open({ duration: 5000, message: ex.message, position: 'is-bottom', type: 'is-danger' }))
    },
    copyKey() {
      // clipboard API needs https; otherwise select the text so it can be copied by hand
      const done = () => this.$buefy.toast.open({ message: 'API key copied', type: 'is-success', duration: 2000 })
      if (navigator.clipboard && window.isSecureContext) {
        navigator.clipboard.writeText(this.createdKey).then(done)
      } else {
        const r = document.createRange()
        r.selectNodeContents(this.$el.querySelector('.created-key'))
        window.getSelection().removeAllRanges()
        window.getSelection().addRange(r)
        document.execCommand('copy') && done()
      }
    },
    revokeApiKey(k) {
      this.$buefy.dialog.confirm({
        title: 'Revoke API key',
        message: `Anything using "${k.name}" (${k.prefix}…) stops working.`,
        confirmText: 'Revoke',
        type: 'is-danger',
        onConfirm: () => axios.delete(`/api/me/apikeys/${k.id}`).then(() => this.fetchApiKeys()),
      })
    },
    formatCurrency(option) {
      return `${option.namePlural} (${option.code})`
    },
  },
}
</script>

<template>
  <Layout>
    <h1 class="title">Your Settings</h1>
    <div class="columns"
      ><div class="column">
        <form class="box " @submit.prevent="saveSettings">
          <h1 class="subtitle">
            These will be used as default values whenever you create a new fillup or expense.
          </h1>
          <b-field label="Currency">
            <b-autocomplete
              v-model="settingsModel.currency"
              :custom-formatter="formatCurrency"
              placeholder="Currency"
              :data="filteredCurrencyMasters"
              :keep-first="true"
              :open-on-focus="true"
              required
              @select="(option) => (selected = option)"
            ></b-autocomplete>
          </b-field>
          <b-field label="Distance Unit">
            <b-select v-model.number="settingsModel.distanceUnit" placeholder="Distance Unit" required expanded>
              <option v-for="(option, key) in distanceUnitMasters" :key="key" :value="key">
                {{ `${option.long} (${option.short})` }}
              </option>
            </b-select>
          </b-field>
          <b-field label="Date Format">
            <b-select v-model.number="settingsModel.dateFormat" placeholder="Date Format" required expanded>
              <option v-for="option in dateFormatMasters" :key="option" :value="option">
                {{ `${option}` }}
              </option>
            </b-select>
          </b-field>
          <br />
          <b-field label="Theme">
            <b-select v-model="settingsModel.theme" expanded>
              <option value="auto">Auto (follow this device)</option>
              <option value="light">Light</option>
              <option value="dark">Dark</option>
            </b-select>
          </b-field>
          <b-field>
            <b-checkbox v-model="settingsModel.showFillupTime">Show the time of fill-ups (e.g. several on one day)</b-checkbox>
          </b-field>
          <b-field>
            <b-checkbox v-model="settingsModel.prefillFillup">Pre-fill new fill-ups with the last price and station</b-checkbox>
          </b-field>
          <b-field label="Stations nearby (fill-up form)" message="Needs fuel prices set up by an admin (Site Settings)">
            <b-select v-model="settingsModel.stationSuggestions" expanded>
              <option value="location">Button, plus stations near a detected fill-up's location</option>
              <option value="button">Only the "Stations nearby" button</option>
              <option value="off">Off</option>
            </b-select>
          </b-field>
          <b-field label="When a detected fill-up matches one I already entered">
            <b-select v-model="settingsModel.detectionDuplicateMode" expanded>
              <option value="ask">Ask me</option>
              <option value="manual">My entry wins (ignore the detection)</option>
              <option value="detection">Use the detected odometer and quantity (keep my price)</option>
            </b-select>
          </b-field>
          <b-field>
            <b-button tag="input" native-type="submit" :disabled="tryingToSave" type="is-primary" value="Save" expanded> </b-button>
          </b-field>
        </form>
      </div>
      <div class="column">
        <form class="box" @submit.prevent="changePassword">
          <h1 class="subtitle">Change password</h1>
          <b-field label="Old Password">
            <b-input v-model="changePassModel.old" required minlength="6" password-reveal type="password"></b-input>
          </b-field>
          <b-field label="New Password">
            <b-input v-model="changePassModel.new" required minlength="6" password-reveal type="password"></b-input>
          </b-field>
          <b-field label="Repeat New Password">
            <b-input v-model="changePassModel.renew" required minlength="6" password-reveal type="password"></b-input>
          </b-field>
          <p v-if="!passwordValid" class="help is-danger">Password values don't match</p>
          <b-field>
            <b-button tag="input" native-type="submit" :disabled="!passwordValid" type="is-primary" value="Change Password" expanded> </b-button>
          </b-field>
        </form>
      </div>
    </div>
    <hr />
    <div class="columns">
      <div class="column">
        <div class="box backup">
          <h1 class="subtitle">Backup &amp; export</h1>
          <p class="mb-3"
            >Each vehicle page has an <strong>export</strong> button (CSV with all fill-ups and expenses). The file can be
            imported again under <router-link to="/import">Import → Fuelly</router-link>: entries that already exist are
            skipped, so it also works as a restore.</p
          >
          <template v-if="isAdmin">
            <p class="mb-3"
              >The full database (all users and vehicles) is also saved automatically every 2 days in
              <code>/config/backups</code>. Restore: stop hammond, unpack the file to <code>/config/hammond.db</code>, start
              hammond.</p
            >
            <b-button
              tag="a"
              type="is-primary"
              class="download-backup"
              :href="`/api/backup?access_token=${currentUser.token}`"
              icon-left="download"
              >Download full backup</b-button
            >
          </template>
        </div>
        <div class="box api-keys">
          <h1 class="subtitle">API keys</h1>
          <p class="mb-3"
            >For Home Assistant, Node-RED or scripts to send detected fill-ups (<code>/api/v1</code>). A key can't do anything
            else.</p
          >
          <b-notification v-if="createdKey" type="is-success" :closable="false">
            Copy the key now: only a fingerprint is stored, so it can't be shown again (lost → create a new one).
            <!-- the whole key, wrapping, instead of a narrow input that cut it off -->
            <div class="is-flex is-align-items-center mt-2" style="gap: 0.5rem">
              <code class="created-key" style="word-break: break-all; flex: 1">{{ createdKey }}</code>
              <b-button type="is-primary" icon-left="copy" @click="copyKey">Copy</b-button>
            </div>
          </b-notification>
          <form @submit.prevent="createApiKey">
            <b-field label="Name">
              <b-input v-model="newKeyName" required maxlength="64" expanded></b-input>
              <p class="control"><b-button tag="input" native-type="submit" type="is-primary" value="Create key"></b-button></p>
            </b-field>
          </form>
          <!-- two columns so it fits a phone: name + details stacked, Revoke on the right -->
          <table v-if="apiKeys.length" class="table is-fullwidth mt-3 api-key-list">
            <tr v-for="k in apiKeys" :key="k.id">
              <td>
                <strong>{{ k.name }}</strong><br />
                <small
                  ><code>{{ k.prefix }}…</code> ·
                  {{ k.lastUsedAt ? 'used ' + parseAndFormatDateTime(k.lastUsedAt) : 'never used' }}</small
                >
              </td>
              <td class="has-text-right" style="vertical-align: middle; white-space: nowrap">
                <b-button size="is-small" type="is-danger" outlined @click="revokeApiKey(k)">Revoke</b-button>
              </td>
            </tr>
          </table>
        </div>
      </div>
    </div>
    <div class="columns">
      <div class="twelve">
        <h3 class="title">More Info</h3>
        <p style="font-style: italic;">
          This project is under active development which means I release new updates very frequently. I will eventually build the version
          management/update checking mechanism. Until then it is recommended that you use something like watchtower which will automatically update
          your containers whenever I release a new version or periodically rebuild the container with the latest image manually.
        </p>
        <br />
        <table class="table is-hoverable">
          <tr>
            <td>Current Version</td>
            <td class="app-version">{{ appVersion }}</td>
          </tr>
          <tr>
            <td>Website</td>
            <td><a :href="repo" target="_blank" rel="noopener noreferrer">{{ repo }}</a></td>
          </tr>
          <tr>
            <td>Found a bug</td>
            <td><a :href="`${repo}/issues`" target="_blank" rel="noopener noreferrer">Report here</a></td>
          </tr>
          <tr>
            <td>Feature Request</td>
            <td><a :href="`${repo}/issues`" target="_blank" rel="noopener noreferrer">Request here</a></td>
          </tr>
          <tr>
            <td>Support this fork</td>
            <td><a href="https://paypal.me/nenadd26" target="_blank" rel="noopener noreferrer">Buy me a coffee (PayPal)</a></td>
          </tr>
          <tr>
            <td>Based on</td>
            <td
              ><a href="https://github.com/akhilrex/hammond" target="_blank" rel="noopener noreferrer">akhilrex/hammond</a> by Akhil
              Gupta ·
              <a href="https://www.buymeacoffee.com/akhilrex" target="_blank" rel="noopener noreferrer">support him</a></td
            >
          </tr>
        </table>
      </div>
    </div>
  </Layout>
</template>
