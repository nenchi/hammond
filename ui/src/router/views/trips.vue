<script>
import Layout from "@layouts/main.vue";
import axios from "axios";
import store from "@state/store";
import { mapState } from "vuex";

const ymd = (d) => {
  const p = (n) => String(n).padStart(2, "0");
  return `${d.getFullYear()}-${p(d.getMonth() + 1)}-${p(d.getDate())}`;
};
// Mercedes (mbapi2020) names: sensor.<plate>_odometer → sensor.<plate>_ignition_state …
const suggestions = {
  ignition: "ignition_state",
  fuelLevel: "fuel_level",
  distance: "distance_start",
  consumption: "liquid_consumption_start",
};

export default {
  page: { title: "Trips" },
  components: { Layout },
  data() {
    const today = new Date();
    return {
      vehicle: null,
      trips: [],
      from: ymd(
        new Date(today.getFullYear(), today.getMonth(), today.getDate() - 30)
      ),
      to: ymd(today),
      loading: false,
      syncing: false,
      setupOpen: false,
      entities: { ignition: "", fuelLevel: "", distance: "", consumption: "" },
      mergeMinutes: 15,
      edit: {}, // trip id → { purpose, notes, business }
      cost: {}, // trip id → { amount, type, comment } while adding a cost
      adding: false,
      manual: {
        start: "",
        end: "",
        startOdo: null,
        endOdo: null,
        purpose: "",
        business: true,
      },
    };
  },
  computed: {
    ...mapState("users", ["me"]),
    isAdmin() {
      return (
        store.state.auth.currentUser &&
        store.state.auth.currentUser.role === "ADMIN"
      );
    },
    locked() {
      const s = store.state.utils.settings;
      return !!(s && s.tripsLocked) && !this.isAdmin;
    },
    cur() {
      return (this.me && this.me.currency) || "";
    },
    km() {
      return (
        (this.me &&
          this.me.distanceUnitDetail &&
          this.me.distanceUnitDetail.short) ||
        "km"
      );
    },
    // trips per day (local), newest day first
    days() {
      const out = [];
      for (const t of this.trips) {
        const day = ymd(new Date(t.start));
        let d = out.find((x) => x.day === day);
        if (!d)
          out.push((d = { day, trips: [], distance: 0, cost: 0, mins: 0 }));
        d.trips.push(t);
        d.distance += t.distance;
        d.cost += t.fuelCost + t.extraCost;
        d.mins += t.driveMinutes;
      }
      return out;
    },
    totals() {
      return this.trips.reduce(
        (a, t) => ({
          distance: a.distance + t.distance,
          business: a.business + (t.business ? t.distance : 0),
          fuel: a.fuel + t.fuelUsed,
          cost: a.cost + t.fuelCost + t.extraCost,
        }),
        { distance: 0, business: 0, fuel: 0, cost: 0 }
      );
    },
  },
  mounted() {
    store.dispatch("utils/getSettings");
    axios.get(`/api/vehicles/${this.$route.params.vehicleId}`).then((r) => {
      this.vehicle = r.data;
      this.entities = Object.assign(
        { ignition: "", fuelLevel: "", distance: "", consumption: "" },
        r.data.tripEntities || {}
      );
      this.setupOpen = !this.entities.ignition;
    });
    store.dispatch("users/me").then((me) => {
      if (me && me.tripMergeMinutes !== undefined)
        this.mergeMinutes = me.tripMergeMinutes;
    });
    this.load();
  },
  methods: {
    toast(message, type = "is-success") {
      this.$buefy.toast.open({
        message,
        type,
        duration: type === "is-success" ? 3000 : 6000,
        position: "is-bottom",
      });
    },
    err(ex) {
      this.toast(
        (ex.response && ex.response.data && ex.response.data.error) ||
          ex.message,
        "is-danger"
      );
    },
    url(path) {
      return `/api/vehicles/${this.$route.params.vehicleId}${path}`;
    },
    load() {
      this.loading = true;
      axios
        .get(this.url("/trips"), { params: { from: this.from, to: this.to } })
        .then((r) => {
          this.trips = r.data || [];
          const edit = {};
          for (const t of this.trips)
            edit[t.id] = {
              purpose: t.purpose,
              notes: t.notes,
              business: t.business,
            };
          this.edit = edit;
        })
        .catch(this.err)
        .finally(() => (this.loading = false));
    },
    sync() {
      this.syncing = true;
      axios
        .post(this.url("/trips/sync"))
        .then((r) => {
          this.toast(
            r.data.added ? `${r.data.added} new drive(s)` : "No new drives"
          );
          this.load();
        })
        .catch(this.err)
        .finally(() => (this.syncing = false));
    },
    suggest() {
      const odo = (this.vehicle && this.vehicle.odometerEntity) || "";
      if (!odo.endsWith("_odometer"))
        return this.toast(
          "Set the odometer entity of the vehicle first (sensor.xyz_odometer)",
          "is-warning"
        );
      const prefix = odo.slice(0, -"odometer".length);
      for (const k in suggestions)
        if (!this.entities[k]) this.entities[k] = prefix + suggestions[k];
    },
    saveSetup() {
      Promise.all([
        axios.put(this.url("/tripEntities"), this.entities),
        axios.post("/api/me/tripSettings", {
          mergeMinutes: Number(this.mergeMinutes),
        }),
      ])
        .then(() => {
          this.toast("Saved");
          store.dispatch("users/forceMe");
        })
        .catch(this.err);
    },
    saveTrip(t) {
      axios
        .put(`/api/trips/${t.id}`, this.edit[t.id])
        .then(() => {
          Object.assign(t, this.edit[t.id]);
          this.toast("Saved");
        })
        .catch(this.err);
    },
    act(method, path, message) {
      axios[method](path)
        .then(() => {
          this.toast(message);
          this.load();
        })
        .catch(this.err);
    },
    del(t) {
      this.$buefy.dialog.confirm({
        message:
          "Delete this trip and its drives? Its costs stay as normal expenses.",
        type: "is-danger",
        onConfirm: () =>
          this.act("delete", `/api/trips/${t.id}`, "Trip deleted"),
      });
    },
    startCost(t) {
      this.$set(this.cost, t.id, { amount: null, type: "Toll", comment: "" });
    },
    addCost(t) {
      const c = this.cost[t.id];
      axios
        .post(`/api/trips/${t.id}/costs`, {
          amount: Number(c.amount),
          type: c.type,
          comment: c.comment,
        })
        .then(() => {
          this.$delete(this.cost, t.id);
          this.toast("Cost added");
          this.load();
        })
        .catch(this.err);
    },
    addManual() {
      const m = this.manual;
      axios
        .post(this.url("/trips"), {
          start: new Date(m.start).toISOString(),
          end: new Date(m.end).toISOString(),
          startOdo: Number(m.startOdo),
          endOdo: Number(m.endOdo),
          purpose: m.purpose,
          business: m.business,
        })
        .then(() => {
          this.adding = false;
          this.toast("Trip added");
          this.load();
        })
        .catch(this.err);
    },
    exportCSV() {
      axios
        .get(this.url("/trips.csv"), {
          params: { from: this.from, to: this.to },
          responseType: "blob",
        })
        .then((r) => {
          const a = document.createElement("a");
          a.href = URL.createObjectURL(r.data);
          a.download = `trips-${this.from}-${this.to}.csv`;
          a.click();
          URL.revokeObjectURL(a.href);
        })
        .catch(this.err);
    },
    print() {
      window.print();
    },
    time(d) {
      return new Date(d).toLocaleTimeString([], {
        hour: "2-digit",
        minute: "2-digit",
      });
    },
    dayName(day) {
      return new Date(day + "T12:00:00").toLocaleDateString([], {
        weekday: "short",
        day: "numeric",
        month: "short",
        year: "numeric",
      });
    },
    dur(mins) {
      return mins >= 60
        ? `${Math.floor(mins / 60)} h ${mins % 60} min`
        : `${mins} min`;
    },
    money(n) {
      return `${(n || 0).toFixed(2)} ${this.cur}`;
    },
  },
};
</script>

<template>
  <Layout>
    <div class="trips-page">
      <h1 class="title">
        Trips<span v-if="vehicle"> · {{ vehicle.nickname }}</span>
      </h1>
      <p class="subtitle is-6 no-print">
        Drives from Home Assistant (engine on → off), joined into one trip when
        the stop between them is short. Fuel is priced first in, first out: the
        oldest fuel in the tank at the price it was bought for.
      </p>
      <p class="print-only">
        {{ vehicle && vehicle.registration }} · {{ from }} – {{ to }} ·
        {{ totals.distance.toFixed(1) }} {{ km }} ({{
          totals.business.toFixed(1)
        }}
        business) · {{ money(totals.cost) }}
      </p>

      <div class="box no-print">
        <div class="columns is-multiline is-vcentered">
          <div class="column is-narrow">
            <b-field label="From"
              ><input v-model="from" class="input" type="date" @change="load"
            /></b-field>
          </div>
          <div class="column is-narrow">
            <b-field label="To"
              ><input v-model="to" class="input" type="date" @change="load"
            /></b-field>
          </div>
          <div class="column buttons">
            <b-button :loading="syncing" icon-left="sync" @click="sync"
              >Sync now</b-button
            >
            <b-button icon-left="file-csv" @click="exportCSV">CSV</b-button>
            <b-button
              icon-left="print"
              title="Print or save as PDF"
              @click="print"
              >Print / PDF</b-button
            >
            <b-button v-if="!locked" icon-left="plus" @click="adding = !adding"
              >Add trip</b-button
            >
            <b-button icon-left="cog" @click="setupOpen = !setupOpen"
              >Setup</b-button
            >
          </div>
        </div>
        <p>
          <strong>{{ totals.distance.toFixed(1) }} {{ km }}</strong> ({{
            totals.business.toFixed(1)
          }}
          business) · {{ totals.fuel.toFixed(1) }} l ·
          <strong>{{ money(totals.cost) }}</strong>
        </p>
      </div>

      <div v-if="setupOpen" class="box no-print setup">
        <h2 class="subtitle">Home Assistant sensors</h2>
        <p class="help mb-3">
          Uses your Home Assistant from Settings and the odometer entity of the
          vehicle. Ignition is required; the trip computer (distance and
          consumption since start) makes kilometres and fuel more exact, the
          fuel level is the fallback.
        </p>
        <b-field
          v-for="(label, k) in {
            ignition: 'Ignition',
            fuelLevel: 'Fuel level (%)',
            distance: 'Trip distance since start',
            consumption: 'Trip consumption since start (l/100 km)',
          }"
          :key="k"
          :label="label"
        >
          <b-input
            v-model.trim="entities[k]"
            pattern="[a-z0-9_]+\.[a-z0-9_]+"
            maxlength="100"
            :has-counter="false"
            placeholder="sensor.…"
          />
        </b-field>
        <b-field
          label="Join drives into one trip when the stop is at most (minutes)"
        >
          <b-input
            v-model.number="mergeMinutes"
            type="number"
            min="0"
            max="1440"
          />
        </b-field>
        <div class="buttons">
          <b-button @click="suggest">Suggest from odometer entity</b-button>
          <b-button type="is-primary" @click="saveSetup">Save</b-button>
        </div>
      </div>

      <div v-if="adding" class="box no-print">
        <h2 class="subtitle">Add a trip by hand</h2>
        <div class="columns is-multiline">
          <div class="column is-3">
            <b-field label="Start"
              ><input
                v-model="manual.start"
                class="input"
                type="datetime-local"
            /></b-field>
          </div>
          <div class="column is-3">
            <b-field label="End"
              ><input v-model="manual.end" class="input" type="datetime-local"
            /></b-field>
          </div>
          <div class="column is-3">
            <b-field :label="`Odometer start (${km})`"
              ><b-input v-model.number="manual.startOdo" type="number"
            /></b-field>
          </div>
          <div class="column is-3">
            <b-field :label="`Odometer end (${km})`"
              ><b-input v-model.number="manual.endOdo" type="number"
            /></b-field>
          </div>
          <div class="column is-9">
            <b-field label="Purpose"
              ><b-input v-model="manual.purpose" maxlength="200"
            /></b-field>
          </div>
          <div class="column is-3">
            <b-field label="Business"
              ><b-switch v-model="manual.business"
            /></b-field>
          </div>
        </div>
        <b-button type="is-primary" @click="addManual">Add</b-button>
      </div>

      <b-loading :active="loading" :is-full-page="false" />
      <div v-if="!loading && !trips.length" class="box">
        No trips in this period.
      </div>

      <section v-for="d in days" :key="d.day" class="day">
        <h2 class="subtitle is-5 day-head">
          {{ dayName(d.day) }}
          <small
            >· {{ d.distance.toFixed(1) }} {{ km }} · {{ dur(d.mins) }} ·
            {{ money(d.cost) }}</small
          >
        </h2>
        <div
          v-for="t in d.trips"
          :key="t.id"
          class="card trip"
          :class="{ business: t.business }"
        >
          <div class="card-content">
            <div class="columns is-multiline">
              <div class="column is-5">
                <p class="is-size-5">
                  <strong>{{ time(t.start) }} – {{ time(t.end) }}</strong> ·
                  {{ t.distance.toFixed(1) }} {{ km }}
                </p>
                <p>
                  {{ t.startOdo.toFixed(0) }} → {{ t.endOdo.toFixed(0) }}
                  {{ km }} · {{ dur(t.driveMinutes) }} driving
                </p>
                <p>
                  ⛽ {{ t.fuelUsed ? t.fuelUsed.toFixed(2) + " l" : "–"
                  }}<span v-if="!t.fuelKnown && t.fuelUsed"> (part)</span> ·
                  {{ money(t.fuelCost)
                  }}<span v-if="t.distance && t.fuelUsed">
                    · {{ ((100 * t.fuelUsed) / t.distance).toFixed(1) }} l/100
                    {{ km }}</span
                  >
                </p>
                <p v-for="c in t.costs" :key="c.id">
                  💶 {{ c.expenseType }} {{ money(c.amount) }}
                  <span class="has-text-grey">{{ c.comments }}</span>
                </p>
                <p>
                  <strong>Total {{ money(t.fuelCost + t.extraCost) }}</strong>
                </p>
              </div>
              <div class="column is-7">
                <b-field grouped>
                  <b-switch v-model="edit[t.id].business" class="no-print"
                    >Business</b-switch
                  >
                  <span class="print-only">{{
                    t.business ? "Business" : "Private"
                  }}</span>
                </b-field>
                <b-field
                  ><b-input
                    v-model="edit[t.id].purpose"
                    placeholder="Purpose (customer, place…)"
                    maxlength="200"
                    :has-counter="false"
                /></b-field>
                <b-field
                  ><b-input
                    v-model="edit[t.id].notes"
                    type="textarea"
                    rows="2"
                    placeholder="Notes"
                    maxlength="2000"
                    :has-counter="false"
                /></b-field>
                <div class="buttons no-print">
                  <b-button
                    size="is-small"
                    type="is-primary"
                    @click="saveTrip(t)"
                    >Save</b-button
                  >
                  <b-button size="is-small" @click="startCost(t)"
                    >Add cost</b-button
                  >
                  <b-button
                    size="is-small"
                    title="Join with the trip before"
                    @click="act('post', `/api/trips/${t.id}/join`, 'Joined')"
                    >Join with previous</b-button
                  >
                  <b-button
                    v-if="!locked"
                    size="is-small"
                    type="is-danger"
                    outlined
                    @click="del(t)"
                    >Delete</b-button
                  >
                </div>
                <div v-if="cost[t.id]" class="columns is-mobile no-print">
                  <div class="column">
                    <b-input
                      v-model.number="cost[t.id].amount"
                      type="number"
                      step="0.01"
                      min="0"
                      :placeholder="cur"
                    />
                  </div>
                  <div class="column">
                    <b-select v-model="cost[t.id].type" expanded>
                      <option
                        v-for="o in [
                          'Toll',
                          'Vignette',
                          'Tunnel',
                          'Parking',
                          'Ferry',
                          'Other',
                        ]"
                        :key="o"
                      >
                        {{ o }}
                      </option>
                    </b-select>
                  </div>
                  <div class="column">
                    <b-input
                      v-model="cost[t.id].comment"
                      placeholder="Comment"
                    />
                  </div>
                  <div class="column is-narrow">
                    <b-button
                      type="is-primary"
                      :disabled="!cost[t.id].amount"
                      @click="addCost(t)"
                      >Add</b-button
                    >
                  </div>
                </div>
              </div>
            </div>
            <details v-if="t.legs.length > 1" class="legs">
              <summary>{{ t.legs.length }} drives</summary>
              <p v-for="(l, i) in t.legs" :key="l.id">
                {{ time(l.start) }}–{{ time(l.end) }} ·
                {{ l.distance.toFixed(1) }} {{ km }}
                <span v-if="l.fuelUsed"
                  >· {{ l.fuelUsed.toFixed(2) }} l ·
                  {{ money(t.legCosts[i]) }}</span
                >
                <a
                  v-if="i > 0"
                  class="no-print"
                  @click="act('post', `/api/legs/${l.id}/split`, 'Split')"
                  >· split here</a
                >
              </p>
            </details>
          </div>
        </div>
      </section>
    </div>
  </Layout>
</template>

<style>
/* plain (not scoped): print rules must reach the navbar */
.trips-page .trip {
  margin-bottom: 0.75rem;
  border-left: 4px solid transparent;
}
.trips-page .trip.business {
  border-left-color: #3273dc;
}
.trips-page .day-head {
  margin: 1.5rem 0 0.5rem;
}
.trips-page .legs {
  margin-top: 0.5rem;
  font-size: 0.9em;
}
.trips-page .print-only {
  display: none;
}
@media print {
  .navbar,
  footer,
  .trips-page .no-print {
    display: none !important;
  }
  .trips-page .print-only {
    display: inline;
  }
  .trips-page .trip {
    break-inside: avoid;
    box-shadow: none;
    border: 1px solid #ccc;
  }
}
</style>
