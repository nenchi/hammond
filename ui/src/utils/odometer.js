// Odometer typo check (warning only). A gap is "too big" above 3x the median gap between fill-ups
// (fixed 1500 until there are 3 gaps). ponytail: tank range x consumption would be tighter.
export function gapLimit(fillups) {
  const odos = fillups.map((f) => f.odoReading).sort((a, b) => a - b)
  const gaps = odos.slice(1).map((o, i) => o - odos[i]).filter((g) => g > 0).sort((a, b) => a - b)
  const median = gaps.length >= 3 ? gaps[Math.floor(gaps.length / 2)] : null
  return { median, limit: median ? median * 3 : 1500 }
}

// Entries out of line with BOTH neighbours by date (only one neighbour at the ends), so a typo is
// flagged and not the correct entry next to it. "Missed the entry before" excuses a big gap.
export function suspiciousFillups(fillups) {
  const { limit } = gapLimit(fillups)
  const byDate = [...fillups].sort((a, b) => new Date(a.date) - new Date(b.date) || a.odoReading - b.odoReading)
  const bad = (a, b) => b.odoReading < a.odoReading || (b.odoReading - a.odoReading > limit && !b.hasMissedFillup)
  return byDate.filter((f, i) => {
    const prev = i > 0 ? bad(byDate[i - 1], f) : null
    const next = i < byDate.length - 1 ? bad(f, byDate[i + 1]) : null
    return (prev ?? next) && (next ?? prev)
  })
}
