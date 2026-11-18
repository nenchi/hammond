<script>
import { Line } from 'vue-chartjs'
import { chartIn, themeColors } from '@utils/charts'

// Price history of one watched station on a time axis: a step line (the price holds until it changes),
// the limit dashed, my fill-ups there as triangles. Wheel / pinch zooms, dragging pans; in "select"
// mode dragging marks a period to zoom into.
export default {
  components: { LineChart: Line }, // not <Line>: Vue 2 takes that for the SVG <line> element
  props: {
    history: { type: Object, required: true }, // { prices: [{at, price}], fillups: [{at, price}] }
    below: { type: Number, default: null },
    select: { type: Boolean, default: false },
  },
  computed: {
    points() {
      const pts = this.history.prices.map((p) => ({ x: new Date(p.at).getTime(), y: p.price }))
      if (pts.length) pts.push({ x: Date.now(), y: pts[pts.length - 1].y }) // the last price holds until now
      return pts
    },
    fills() {
      return this.history.fillups.map((f) => ({ x: new Date(f.at).getTime(), y: f.price }))
    },
    range() {
      const xs = this.points.concat(this.fills).map((p) => p.x)
      return { min: Math.min(...xs), max: Math.max(...xs) }
    },
    chartData() {
      const datasets = [
        { label: 'Price', stepped: true, fill: false, borderColor: '#39a275', borderWidth: 2, pointRadius: 0, pointHitRadius: 6, data: this.points },
      ]
      if (this.below && this.points.length)
        datasets.push({
          label: 'Your limit',
          borderColor: '#e0a030',
          borderDash: [6, 4],
          borderWidth: 2,
          pointRadius: 0,
          data: [
            { x: this.range.min, y: this.below },
            { x: this.range.max, y: this.below },
          ],
        })
      if (this.fills.length)
        datasets.push({
          type: 'scatter',
          label: 'My fill-ups',
          pointStyle: 'triangle',
          pointRadius: 7,
          backgroundColor: '#3273dc',
          borderColor: '#3273dc',
          data: this.fills,
        })
      return { datasets }
    },
    chartOptions() {
      const { text, grid } = themeColors()
      return {
        maintainAspectRatio: false,
        animation: false,
        color: text,
        scales: {
          x: {
            type: 'time',
            min: this.range.min,
            max: this.range.max,
            time: { tooltipFormat: 'dd MMM yyyy HH:mm', displayFormats: { hour: 'HH:mm', day: 'dd MMM', month: 'MMM yyyy' } },
            ticks: { color: text, maxRotation: 0, autoSkipPadding: 20 },
            grid: { color: grid },
          },
          y: { ticks: { color: text, callback: (v) => Number(v).toFixed(3) }, grid: { color: grid } },
        },
        plugins: {
          legend: { labels: { color: text } },
          tooltip: { callbacks: { label: (c) => `${c.dataset.label}: ${Number(c.parsed.y).toFixed(3)}` } },
          zoom: {
            limits: { x: { min: 'original', max: 'original' } }, // not beyond the data
            pan: { enabled: !this.select, mode: 'x' },
            zoom: { wheel: { enabled: true }, pinch: { enabled: true }, drag: { enabled: this.select }, mode: 'x' },
          },
        },
      }
    },
  },
  methods: {
    resetZoom() {
      const c = chartIn(this.$el)
      if (c) c.resetZoom()
    },
  },
}
</script>

<template>
  <!-- relative: Chart.js sizes the canvas to this box (the height comes from the page) -->
  <div style="position: relative">
    <LineChart :data="chartData" :options="chartOptions" />
  </div>
</template>
