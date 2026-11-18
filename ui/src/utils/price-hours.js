// Average price per hour of the day (0–23, local time), weighted by how long each price held: a price
// change is stored only when it happens, so the price holds until the next point (the last one until
// `end`). Hours the data never covered are null.
export function averageByHour(points, end = Date.now()) {
  const sum = new Array(24).fill(0)
  const time = new Array(24).fill(0)
  for (let i = 0; i < points.length; i++) {
    let t = new Date(points[i].at).getTime()
    const until = i + 1 < points.length ? new Date(points[i + 1].at).getTime() : end
    while (t < until) {
      const d = new Date(t)
      const nextHour = new Date(d.getFullYear(), d.getMonth(), d.getDate(), d.getHours() + 1).getTime()
      const step = Math.min(nextHour, until) - t
      sum[d.getHours()] += points[i].price * step
      time[d.getHours()] += step
      t += step
    }
  }
  return sum.map((s, h) => (time[h] ? s / time[h] : null))
}
