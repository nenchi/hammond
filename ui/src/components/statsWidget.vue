<script>
import { addDays, addMonths } from 'date-fns'
import currencyFormtter from 'currency-formatter'
import { mapState } from 'vuex'

import axios from 'axios'
// "2026-11-24" in local time, for <input type="date">
const ymd = (d) => `${d.getFullYear()}-${String(d.getMonth() + 1).padStart(2, '0')}-${String(d.getDate()).padStart(2, '0')}`

export default {
  props: {
    user: {
      type: Object,
      required: true,
    },
  },
  data: function() {
    return {
      dateRangeOptions: [
        { label: 'This week', value: 'this_week' },
        { label: 'This month', value: 'this_month' },
        { label: 'Past 30 days', value: 'past_30_days' },
        { label: 'Past 3 months', value: 'past_3_months' },
        { label: 'This year', value: 'this_year' },
        { label: 'All Time', value: 'all_time' },
        { label: 'Custom…', value: 'custom' },
      ],
      dateRangeOption: 'past_30_days',
      customFrom: ymd(new Date(Date.now() - 30 * 864e5)),
      customTo: ymd(new Date()),
      stats: [],
    }
  },
  computed: {
    ...mapState('utils', ['isMobile']),
    summaryObject() {
      if (this.stats == null) {
        return [
          [
            {
              label: 'Total Expenditure',
              value: this.formatCurrency(0, this.user.currency),
            },
            {
              label: 'Fillup Costs',
              value: `${this.formatCurrency(0, this.user.currency)} (0)`,
            },
            {
              label: 'Other Expenses',
              value: `${this.formatCurrency(0, this.user.currency)} (0)`,
            },
          ],
        ]
      }
      return this.stats.map((x) => {
        return [
          {
            label: 'Total Expenditure',
            value: this.formatCurrency(x.expenditureTotal, x.currency),
          },
          {
            label: 'Fillup Costs',
            value: `${this.formatCurrency(x.expenditureFillups, x.currency)} (${x.countFillups})`,
          },
          {
            label: 'Other Expenses',
            value: `${this.formatCurrency(x.expenditureExpenses, x.currency)} (${x.countExpenses})`,
          },
        ]
      })
    },
  },
  watch: {
    dateRangeOption(newOne, old) {
      if (newOne === old) {
        return
      }
      this.getStats()
    },
    customFrom() {
      if (this.dateRangeOption === 'custom') this.getStats()
    },
    customTo() {
      if (this.dateRangeOption === 'custom') this.getStats()
    },
  },
  mounted() {
    this.getStats()
  },
  methods: {
    formatCurrency(number, currencyCode) {
      if (!currencyCode) {
        currencyCode = this.me.currency
      }
      return currencyFormtter.format(number, { code: currencyCode })
    },
    getStats() {
      axios
        .get('/api/me/stats', {
          params: {
            start: this.getStartDate(),
            end: this.dateRangeOption === 'custom' ? new Date(this.customTo + 'T23:59:59') : new Date(),
          },
        })
        .then((data) => {
          this.stats = data.data
        })
    },
    getStartDate() {
      const toDate = new Date()
      switch (this.dateRangeOption) {
        case 'this_week':
          var currentDayOfWeek = toDate.getDay()
          var toSubtract = 0
          if (currentDayOfWeek === 0) {
            toSubtract = -6
          }
          if (currentDayOfWeek > 1) {
            toSubtract = -1 * (currentDayOfWeek - 1)
          }
          toDate.setHours(0, 0, 0, 0)
          return addDays(toDate, toSubtract)
        case 'this_month':
          return new Date(toDate.getFullYear(), toDate.getMonth(), 1)
        case 'past_30_days':
          return addDays(toDate, -30)
        case 'past_3_months':
          return addMonths(toDate, -3)
        case 'this_year':
          return new Date(toDate.getFullYear(), 0, 1)
        case 'all_time':
          return new Date(1969, 4, 20)
        case 'custom':
          return new Date(this.customFrom + 'T00:00:00')
        default:
          return new Date(1969, 4, 20)
      }
    },
  },
}
</script>

<template>
  <div>
    <div class="columns">
      <div class="column" :class="isMobile ? 'has-text-centered' : ''"> <h1 class="title">Stats</h1></div>
      <div class="column">
        <b-select v-model="dateRangeOption" class="is-pulled-right is-medium">
          <option v-for="option in dateRangeOptions" :key="option.value" :value="option.value">
            {{ option.label }}
          </option>
        </b-select>
        <div v-if="dateRangeOption === 'custom'" class="is-flex is-justify-content-flex-end stats-custom" style="gap: 0.5rem; clear: both; padding-top: 0.5rem">
          <input v-model="customFrom" class="input" type="date" :max="customTo" title="From" style="width: auto" />
          <input v-model="customTo" class="input" type="date" :min="customFrom" title="To" style="width: auto" />
        </div></div
      >
    </div>
    <div v-for="(currencyLevel, index) in summaryObject" :key="index" class="level box">
      <div v-for="item in currencyLevel" :key="item.label" class="level-item has-text-centered">
        <div>
          <p class="heading">{{ item.label }}</p>
          <p class="title is-4">{{ item.value }}</p>
        </div>
      </div>
    </div>
  </div>
</template>
