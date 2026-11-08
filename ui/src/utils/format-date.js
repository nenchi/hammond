// https://date-fns.org/docs/format
import { format, parseISO } from 'date-fns'

export default function formatDate(date) {
  return format(date, 'MMM do, yyyy')
}

export function parseAndFormatDate(date) {
  return format(parseISO(date), 'MMM dd, yyyy')
}

export function parseAndFormatDateTime(date) {
  return format(parseISO(date), 'MMM dd, yyyy hh:mm aa')
}

// fill-ups: "Nov 07, 2026 18:30", or only the date if the setting is off or the entry has no time
// (older entries were saved at midnight)
export function parseAndFormatFillupDate(date, showTime = true) {
  const d = typeof date === 'string' ? parseISO(date) : date
  const hasTime = d.getHours() !== 0 || d.getMinutes() !== 0
  return format(d, showTime && hasTime ? 'MMM dd, yyyy HH:mm' : 'MMM dd, yyyy')
}

// "HH:mm" of a date, '' for midnight (= no time entered)
export function timeOf(date) {
  const d = typeof date === 'string' ? parseISO(date) : date
  return d.getHours() !== 0 || d.getMinutes() !== 0 ? format(d, 'HH:mm') : ''
}
