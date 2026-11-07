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
