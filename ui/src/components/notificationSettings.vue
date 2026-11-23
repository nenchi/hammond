<script>
import axios from "axios";

// Notification channels: where price alerts go (Gotify, ntfy, Matrix, Telegram). Each can be turned off,
// edited, tested and deleted; a Telegram channel can also answer the fuel commands in its chat.
const kinds = {
  gotify: {
    label: "Gotify",
    url: "Server URL",
    urlHint: "https://gotify.example.com",
    secret: "App token",
    secretHint: "Gotify → Apps → create an app → its token",
  },
  ntfy: {
    label: "ntfy",
    url: "Topic URL",
    urlHint: "https://ntfy.sh/my-hammond-alerts",
    secret: "Access token (optional)",
    secretHint: "Only for protected topics (tk_…)",
  },
  matrix: {
    label: "Matrix",
    url: "Homeserver URL",
    urlHint: "https://matrix.example.com",
    secret: "Access token",
    secretHint:
      "Of the account that posts (a bot account is best): Element → Settings → Help & About → Access token",
    room: true,
  },
  telegram: {
    label: "Telegram",
    secret: "Bot token",
    secretHint: "Telegram → @BotFather → /newbot → the token (123456789:AA…)",
  },
};
const blank = (kind) => ({
  kind,
  name: "",
  enabled: true,
  url: "",
  secret: "",
  room: "",
  commands: kind === "telegram",
});

export default {
  data() {
    return {
      kinds,
      channels: [],
      form: null,
      editing: null,
      results: {},
      saving: false,
    };
  },
  mounted() {
    this.load();
  },
  methods: {
    errorText(ex) {
      return (
        (ex.response &&
          ex.response.data &&
          (ex.response.data.error ||
            JSON.stringify(ex.response.data.errors))) ||
        ex.message
      );
    },
    toast(message, ok = true) {
      this.$buefy.toast.open(
        ok
          ? { message, type: "is-success", duration: 3000 }
          : {
              message,
              duration: 6000,
              position: "is-bottom",
              type: "is-danger",
            }
      );
    },
    load() {
      return axios
        .get("/api/me/notifications")
        .then(({ data }) => (this.channels = data));
    },
    add(kind) {
      this.editing = null;
      this.form = blank(kind);
    },
    edit(c) {
      this.editing = c.id;
      this.form = {
        kind: c.kind,
        name: c.name,
        enabled: c.enabled,
        url: c.url,
        secret: "",
        room: c.room,
        commands: c.commands,
        secretSet: c.secretSet,
      };
    },
    save() {
      this.saving = true;
      const req = this.editing
        ? axios.put(`/api/me/notifications/${this.editing}`, this.form)
        : axios.post("/api/me/notifications", this.form);
      req
        .then(() => {
          this.toast("Saved");
          this.form = null;
          this.editing = null;
          return this.load();
        })
        .catch((ex) => this.toast(this.errorText(ex), false))
        .finally(() => (this.saving = false));
    },
    toggle(c) {
      axios
        .put(`/api/me/notifications/${c.id}`, {
          ...c,
          enabled: !c.enabled,
          secret: "",
        })
        .then(() => this.load())
        .catch((ex) => this.toast(this.errorText(ex), false));
    },
    test(c) {
      this.$set(this.results, c.id, { testing: true });
      axios
        .post(`/api/me/notifications/${c.id}/test`)
        .then(({ data }) => this.$set(this.results, c.id, data))
        .catch((ex) =>
          this.$set(this.results, c.id, {
            ok: false,
            error: this.errorText(ex),
          })
        );
    },
    remove(c) {
      this.$buefy.dialog.confirm({
        message: `Delete the channel "${c.name}"?`,
        type: "is-danger",
        confirmText: "Delete",
        onConfirm: () =>
          axios
            .delete(`/api/me/notifications/${c.id}`)
            .then(() => this.load())
            .catch((ex) => this.toast(this.errorText(ex), false)),
      });
    },
    unlink(c) {
      axios
        .post(`/api/me/notifications/${c.id}/unlink`)
        .then(() => this.load());
    },
  },
};
</script>

<template>
  <div class="box notifications">
    <h1 class="subtitle">Notifications</h1>
    <p class="mb-3">
      Where price alerts go: every channel that is on gets them. Telegram can
      also answer the fuel commands (<code>prices</code>,
      <code>graph fuel 30</code>, <code>trips 10 pdf</code>…) in its chat.
    </p>

    <div v-for="c in channels" :key="c.id" class="channel" :data-kind="c.kind">
      <div class="level is-mobile mb-1">
        <div class="level-left">
          <b-switch
            :value="c.enabled"
            class="channel-enabled"
            @input="toggle(c)"
          />
          <strong>{{ c.name }}</strong>
          <span class="tag ml-2">{{ kinds[c.kind].label }}</span>
        </div>
        <div class="level-right buttons">
          <b-button
            size="is-small"
            class="test-channel"
            :loading="results[c.id] && results[c.id].testing"
            @click="test(c)"
            >Test</b-button
          >
          <b-button size="is-small" @click="edit(c)">Edit</b-button>
          <b-button
            size="is-small"
            type="is-danger"
            outlined
            class="delete-channel"
            @click="remove(c)"
            >Delete</b-button
          >
        </div>
      </div>
      <p class="help">
        <span v-if="c.url">{{ c.url }}</span
        ><span v-if="c.room"> · {{ c.room }}</span><span v-if="c.kind === 'matrix' && c.commands"> · Fuel room (homebot)</span>
        <template v-if="c.kind === 'telegram'">
          <span v-if="c.bot">{{ c.bot }} · </span>
          <span v-if="c.linked"
            >chat linked<span v-if="c.commands">, answers commands</span> ·
            <a @click="unlink(c)">unlink</a></span
          >
          <span v-else class="has-text-warning-dark telegram-code"
            >not linked yet: send <code>/start {{ c.code }}</code> to
            {{ c.bot || "the bot" }}, then reload this page</span
          >
        </template>
      </p>
      <p
        v-if="results[c.id] && !results[c.id].testing"
        class="help channel-test"
        :class="results[c.id].ok ? 'is-success' : 'is-danger'"
      >
        {{
          results[c.id].ok
            ? "✓ Sent: check " + kinds[c.kind].label
            : "✗ " + results[c.id].error
        }}
      </p>
      <hr />
    </div>
    <p v-if="!channels.length" class="mb-3 has-text-grey">
      No channel yet: price alerts aren't sent anywhere.
    </p>

    <form v-if="form" class="channel-form" @submit.prevent="save">
      <h2 class="subtitle is-6">
        {{ editing ? "Edit" : "New" }} {{ kinds[form.kind].label }} channel
      </h2>
      <b-field label="Name"
        ><b-input
          v-model="form.name"
          :placeholder="kinds[form.kind].label"
          maxlength="60"
          :has-counter="false"
      /></b-field>
      <b-field v-if="kinds[form.kind].url" :label="kinds[form.kind].url"
        ><b-input
          v-model.trim="form.url"
          type="url"
          :placeholder="kinds[form.kind].urlHint"
      /></b-field>
      <b-field
        v-if="kinds[form.kind].room"
        label="Room"
        message="Room ID (!abc:example.com) or alias (#fuel:example.com); invite the account first"
      >
        <b-input v-model.trim="form.room" />
      </b-field>
      <b-field
        :label="kinds[form.kind].secret"
        :message="
          form.secretSet
            ? 'saved, hidden – leave empty to keep it, - removes it'
            : kinds[form.kind].secretHint
        "
      >
        <b-input
          v-model="form.secret"
          type="password"
          password-reveal
          autocomplete="new-password"
        />
      </b-field>
      <b-field><b-switch v-model="form.enabled">On</b-switch></b-field>
      <b-field v-if="form.kind === 'telegram'"
        ><b-switch v-model="form.commands"
          >Answer the fuel commands in the linked chat</b-switch
        ></b-field
      >
      <b-field
        v-if="form.kind === 'matrix'"
        message="homebot relays that room to hammond: you can answer there, e.g. with the price"
        ><b-switch v-model="form.commands">This is homebot's Fuel room: "fill-up detected" goes here too</b-switch></b-field
      >
      <div class="buttons">
        <b-button
          native-type="submit"
          type="is-primary"
          :loading="saving"
          class="save-channel"
          >Save</b-button
        >
        <b-button @click="form = null">Cancel</b-button>
      </div>
    </form>
    <div v-else class="buttons">
      <b-button
        v-for="(k, id) in kinds"
        :key="id"
        :class="'add-' + id"
        icon-left="plus"
        @click="add(id)"
        >{{ k.label }}</b-button
      >
    </div>
  </div>
</template>
