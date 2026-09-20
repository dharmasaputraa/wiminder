const devEmailKey = 'wiminder-dev-email'

export function devEmail(): string | null { return localStorage.getItem(devEmailKey) }

/** Wire id format: every id the API serves (contacts, occasions, channels)
 *  is a UUIDv7 string. UUID_SRC is the bare pattern (no anchors) for
 *  embedding in larger regexes (e.g. URL event ids). */
export const UUID_SRC = '[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}'
export const UUID_RE = new RegExp(`^${UUID_SRC}$`, 'i')

export class ApiError extends Error {
  status: number
  constructor(status: number, message: string) {
    super(message)
    this.status = status
  }
}

export async function api<T>(path: string, init?: RequestInit): Promise<T> {
  const headers = new Headers(init?.headers)
  const email = devEmail()
  if (email) headers.set('X-Dev-Email', email)
  if (init?.body) headers.set('Content-Type', 'application/json')
  const res = await fetch(`/api/v1${path}`, { ...init, headers })
  if (res.status === 401 && !email) {
    const entered = window.prompt('Dev mode: enter your email (admin if listed in ADMIN_EMAILS)')
    if (entered) {
      localStorage.setItem(devEmailKey, entered.trim().toLowerCase())
      return api<T>(path, init)
    }
  }
  if (!res.ok) {
    let msg = res.statusText
    try { msg = (await res.json()).error ?? msg } catch { /* keep statusText */ }
    throw new ApiError(res.status, msg)
  }
  return res.json()
}

export interface UpcomingItem {
  date: string; kind: 'occasion' | 'holiday'
  occasion_id?: string; contact_id?: string; contact_name?: string
  type?: string
  /** Recurrence stream of the occasion. Holidays send `""` — treat it as absent. */
  recurrence?: string
  /** Year/month mark of the occurrence (occasions only; absent when 0). */
  number?: number
  title: string; pawukon?: string; days_until: number
  reminders?: number[]; reminders_default?: boolean
}
export interface Occasion {
  id: string; type: string
  recurrence: 'once' | 'yearly' | 'monthly' | 'anniversary' | 'otonan'
  base_date: string; label: string; prefs: OccasionPrefs | null
}
/** Per-stream reminder offsets, keyed by recurrence stream (event, yearly,
 *  monthly, otonan). Absent or empty list = inherit that stream. */
export type OffsetMap = Record<string, number[]>
export interface Prefs { contact_id: string; offsets: OffsetMap; channel_ids: string[]; enabled: boolean }
export interface OccasionPrefs { occasion_id: string; offsets: OffsetMap; channel_ids: string[]; enabled: boolean; custom: boolean }
export interface Contact { id: string; name: string; nickname: string; notes: string; occasions: Occasion[]; prefs: Prefs | null }
export interface Channel { id: string; type: string; name: string; enabled: boolean }
export interface Settings {
  timezone: string; send_time: string; catch_up_hours: number
  default_offsets: number[]; default_channel_ids: string[] | null
  holiday_categories: Record<string, boolean>
  holiday_offsets: Record<string, number[]>
  /** Per-stream default offset sets (event, yearly, monthly, otonan). */
  recurrence_offsets: Record<string, number[]>
}
