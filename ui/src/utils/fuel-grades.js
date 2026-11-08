// Fuel subtypes per fuel type (db.FuelType: 0 petrol, 1 diesel, 2 ethanol, 3 CNG, 4 electric, 5 LPG).
// A drop-down instead of upstream's free text, so "hbuzg" can't become a fuel. Values are country-neutral
// (the octane number is on every pump); the text shows the local names (DE Super, AT/HR Eurosuper,
// SI NMB, RS/BA BMB, HR/RS Eurodizel), so stats stay comparable across countries.
const OPTIONS = {
  0: [
    ['95', '95 – Super E5 · Eurosuper · NMB · BMB'],
    ['95 E10', '95 E10 – Super E10 · NMB 95 E10'],
    ['98', '98 – Super Plus'],
    ['100', '100 – Premium'],
  ],
  1: [
    ['Diesel', 'Diesel – Eurodizel · Dizel'],
    ['Premium Diesel', 'Premium Diesel'],
    ['HVO100', 'HVO100'],
  ],
  2: [['E85', 'E85 – Bioethanol'], ['95 E10', '95 E10 – Super E10']],
  3: [['CNG', 'CNG – Erdgas · Metan']],
  4: [['AC', 'AC'], ['DC', 'DC']],
  5: [['LPG', 'LPG – Autogas · Autoplin']],
}

// value written by the station picker for a price grade
export const GRADE_LABELS = { e5: '95', e10: '95 E10', diesel: 'Diesel' }

// [value, text] options for a vehicle; an existing value not in the list (old free text, Fuelly import)
// stays selectable so editing an old entry doesn't lose it
export function fuelSubtypes(fuelType, current, custom = []) {
  const list = (OPTIONS[fuelType] || []).slice()
  // the user's own types (Settings → My fuel types), e.g. "Q Max 95"
  for (const t of custom) if (!list.some(([v]) => v === t)) list.push([t, t])
  if (current && !list.some(([v]) => v === current)) list.push([current, current])
  return list
}

// default for a new fill-up: the vehicle's grade for station prices, else the first option
export function defaultFuelSubtype(vehicle) {
  const preferred = (vehicle.fuelGrade || '').split(',')[0]
  if (GRADE_LABELS[preferred]) return GRADE_LABELS[preferred]
  const first = (OPTIONS[vehicle.fuelType] || [])[0]
  return first ? first[0] : ''
}
