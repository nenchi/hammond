<script>
import { Line } from 'vue-chartjs'
import { chartIn, themeColors } from '@utils/charts'

// one colour per grade line (its limit dashed in the same colour)
const COLORS = ['#39a275', '#3273dc', '#e0533d', '#9b59b6', '#e0a030', '#17a2b8']

// Price history of one watched station on a time axis: a step line per watched grade (the price holds until
// it changes), each limit dashed, my fill-ups there as triangles. Wheel / pinch zooms, dragging pans; in
// "select" mode dragging marks a period to zoom into. Tapping a grade in the legend hides it.
export default {
  components: { LineChart: Line }, // not <Line>: Vue 2 takes that for the SVG <line> element
  props: {
    // [{ label, below, history: { prices: [{at, price}], fillups: [{at, price}] } }]
    series: { type: Array, required: true },
    select: { type: Boolean, default: false },
  },
  computed: {
    lines() {
      return this.series.map((s) => {
        const pts = s.history.prices.map((p) => ({ x: new Date(p.at).getTime(), y: p.price }))
        if (pts.length) pts.push({ x: Date.now(), y: pts[pts.length - 1].y }) // the last price holds until now
        return pts
      })
    },
    // the same station for every grade: its fill-ups once
    fills() {
      return ((this.series[0] && this.series[0].history.fillups) || []).map((f) => ({ x: new Date(f.at).getTime(), y: f.price }))
    },
    range() {
      const xs = [].concat(...this.lines, this.fills).map((p) => p.x)
      return { min: Math.min(...xs), max: Math.max(...xs) }
    },
    chartData() {
      const datasets = []
      this.series.forEach((s, i) => {
        const color = COLORS[i % COLORS.length]
        if (!this.lines[i].length) return
        datasets.push({ label: s.label, stepped: true, fill: false, borderColor: color, backgroundColor: color, borderWidth: 2, pointRadius: 0, pointHitRadius: 6, data: this.lines[i] })
        if (s.below)
          datasets.push({
            label: `${s.label} limit`,
            borderColor: color,
            backgroundColor: color,
            borderDash: [6, 4],
            borderWidth: 1.5,
            pointRadius: 0,
            data: [
              { x: this.range.min, y: s.below },
              { x: this.range.max, y: s.below },
            ],
          })
      })
      if (this.fills.length)
        datasets.push({
          type: 'scatter',
          label: 'My fill-ups',
          pointStyle: 'triangle',
          pointRadius: 7,
          backgroundColor: '#7a7a7a',
          borderColor: '#7a7a7a',
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
