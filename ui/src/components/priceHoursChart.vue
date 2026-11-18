<script>
import { Bar } from 'vue-chartjs'
import { themeColors } from '@utils/charts'
import { averageByHour } from '@utils/price-hours'

// "Cheapest time of day": average price per hour over the shown period, the cheapest hours green
export default {
  components: { Bar },
  props: { history: { type: Object, required: true } },
  computed: {
    avg() {
      return averageByHour(this.history.prices)
    },
    low() {
      return Math.min(...this.avg.filter((v) => v !== null))
    },
    high() {
      return Math.max(...this.avg.filter((v) => v !== null))
    },
    chartData() {
      return {
        labels: this.avg.map((_, h) => String(h).padStart(2, '0')),
        datasets: [
          {
            label: 'Average price by hour of day',
            // cheapest = within 0.5 cent of the lowest hour
            backgroundColor: this.avg.map((v) => (v !== null && v <= this.low + 0.005 ? '#39a275' : '#7a8a9a')),
            data: this.avg,
          },
        ],
      }
    },
    chartOptions() {
      const { text, grid } = themeColors()
      return {
        maintainAspectRatio: false,
        animation: false,
        color: text,
        scales: {
          x: { ticks: { color: text, maxRotation: 0, autoSkipPadding: 8 }, grid: { display: false } },
          // not from zero: the differences are cents
          y: {
            min: Math.floor((this.low - 0.01) * 100) / 100,
            max: Math.ceil((this.high + 0.005) * 100) / 100,
            ticks: { color: text, callback: (v) => Number(v).toFixed(2) },
            grid: { color: grid },
          },
        },
        plugins: {
          legend: { labels: { color: text, boxWidth: 0 } },
          tooltip: { callbacks: { title: (i) => `${i[0].label}:00–${i[0].label}:59`, label: (c) => Number(c.parsed.y).toFixed(3) } },
        },
      }
    },
  },
}
</script>

<template>
  <!-- relative: Chart.js sizes the canvas to this box (the height comes from the page) -->
  <div style="position: relative">
    <Bar :data="chartData" :options="chartOptions" />
  </div>
</template>
