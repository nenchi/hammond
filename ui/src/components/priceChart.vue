<script>
import { Line } from 'vue-chartjs'
import 'chartjs-plugin-zoom'
import { format } from 'date-fns'
import { isDark } from '@utils/theme'

// Price history of one watched station on a real time axis: a step line (the price holds until it
// changes), the limit dashed, my fill-ups there as triangles. Mouse wheel / pinch zooms, dragging pans;
// in "select" mode dragging marks a period to zoom into. ponytail: a linear axis of timestamps with
// formatted ticks, because Chart.js 2's time axis needs moment.
export default {
  extends: Line,
  props: {
    history: { type: Object, required: true }, // { prices: [{at, price}], fillups: [{at, price}] }
    below: { type: Number, default: null },
    select: { type: Boolean, default: false },
  },
  watch: {
    history() {
      this.show()
    },
    select() {
      this.show()
    },
  },
  mounted() {
    this.show()
  },
  methods: {
    resetZoom() {
      if (this.$data._chart) this.$data._chart.resetZoom()
    },
    show() {
      const pts = this.history.prices.map((p) => ({ x: new Date(p.at).getTime(), y: p.price }))
      if (pts.length) pts.push({ x: Date.now(), y: pts[pts.length - 1].y }) // the last price holds until now
      const fills = this.history.fillups.map((f) => ({ x: new Date(f.at).getTime(), y: f.price }))
      const xs = pts.concat(fills).map((p) => p.x)
      const min = Math.min(...xs)
      const max = Math.max(...xs)
      const text = isDark() ? '#d6dbe1' : '#666'
      const grid = isDark() ? 'rgba(255, 255, 255, 0.1)' : 'rgba(0, 0, 0, 0.1)'
      const datasets = [
        { label: 'Price', steppedLine: true, fill: false, borderColor: '#39a275', borderWidth: 2, pointRadius: 0, pointHitRadius: 6, data: pts },
      ]
      if (this.below && pts.length)
        datasets.push({
          label: 'Your limit',
          fill: false,
          borderColor: '#e0a030',
          borderDash: [6, 4],
          borderWidth: 2,
          pointRadius: 0,
          data: [{ x: min, y: this.below }, { x: max, y: this.below }],
        })
      if (fills.length)
        datasets.push({
          label: 'My fill-ups',
          showLine: false,
          pointStyle: 'triangle',
          pointRadius: 7,
          pointBackgroundColor: '#3273dc',
          borderColor: '#3273dc',
          data: fills,
        })
      // tick and tooltip format by what's on screen: hours for a day or two, else days
      const tick = (v, all) => format(new Date(v), all < 2 * 86400000 ? 'HH:mm' : all < 300 * 86400000 ? 'dd MMM' : 'MMM yyyy')
      this.renderChart(
        { datasets },
        {
          maintainAspectRatio: false,
          animation: false,
          legend: { labels: { fontColor: text } },
          tooltips: {
            callbacks: {
              title: (items) => format(new Date(items[0].xLabel), 'dd MMM yyyy HH:mm'),
              label: (i) => `${datasets[i.datasetIndex].label}: ${Number(i.yLabel).toFixed(3)}`,
            },
          },
          scales: {
            xAxes: [
              {
                type: 'linear',
                // the first and last date stay; round dates right next to them would overlap them
                afterBuildTicks: (scale) => {
                  const t = scale.ticks
                  const gap = (scale.max - scale.min) * 0.2
                  scale.ticks = t.filter((v, i) => i === 0 || i === t.length - 1 || (v - t[0] > gap && t[t.length - 1] - v > gap))
                },
                ticks: {
                  min, // the axis ends where the data ends, not at the next round date
                  max,
                  fontColor: text,
                  maxRotation: 0,
                  autoSkipPadding: 20,
                  callback: function(v) {
                    return tick(v, this.max - this.min)
                  },
                },
                gridLines: { color: grid, zeroLineColor: grid },
              },
            ],
            yAxes: [{ ticks: { fontColor: text, callback: (v) => v.toFixed(3) }, gridLines: { color: grid, zeroLineColor: grid } }],
          },
          plugins: {
            zoom: {
              // not beyond the data
              pan: { enabled: !this.select, mode: 'x', rangeMin: { x: min }, rangeMax: { x: max } },
              zoom: { enabled: true, drag: this.select, mode: 'x', speed: 0.1, rangeMin: { x: min }, rangeMax: { x: max } },
            },
          },
        }
      )
    },
  },
}
</script>
