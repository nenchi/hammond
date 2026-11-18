<script>
import { Line } from 'vue-chartjs'
import axios from 'axios'
import { themeColors } from '@utils/charts'

// Mileage per fill-up as a step line, loaded once per period
export default {
  components: { LineChart: Line }, // not <Line>: Vue 2 takes that for the SVG <line> element
  props: {
    vehicle: { type: Object, required: true },
    since: { type: Date, default: '' },
    user: { type: Object, required: true },
    styles: { type: Object, default: () => ({}) },
  },
  data() {
    return { points: null }
  },
  computed: {
    chartData() {
      return {
        labels: this.points.map((x) => x.date.substr(0, 10)),
        datasets: [
          {
            label: `Mileage (${this.user.distanceUnitDetail.short}/${this.vehicle.fuelUnitDetail.short})`,
            stepped: true,
            fill: true,
            borderColor: '#39a275',
            backgroundColor: 'rgba(57, 162, 117, 0.25)',
            borderWidth: 2,
            pointBackgroundColor: '#39a275',
            pointRadius: 3,
            data: this.points.map((x) => x.mileage),
          },
        ],
      }
    },
    chartOptions() {
      const { text, grid } = themeColors()
      const axis = { ticks: { color: text }, grid: { color: grid } }
      return {
        maintainAspectRatio: false,
        color: text,
        scales: { x: axis, y: axis },
        plugins: { legend: { labels: { color: text } } },
      }
    },
  },
  watch: {
    since(newOne, old) {
      if (newOne && old && newOne.getTime() === old.getTime()) return
      this.fetchMileage()
    },
  },
  mounted() {
    this.fetchMileage()
  },
  methods: {
    fetchMileage() {
      axios
        .get(`/api/vehicles/${this.vehicle.id}/mileage`, { params: { since: this.since } })
        .then((response) => (this.points = response.data || []))
        .catch((err) => console.log(err))
    },
  },
}
</script>

<template>
  <div :style="styles">
    <LineChart v-if="points" :data="chartData" :options="chartOptions" />
  </div>
</template>
