import type { ForgeClient } from './client'
import { HttpForgeClient, type HttpForgeClientOptions } from './http'
import { createMockForgeClient } from './mock'

export type ForgeMode = 'mock' | 'http'

/**
 * Dev switch mirroring events/mode.ts: `?forge=mock|http` in the URL wins, then
 * VITE_FORGE_SOURCE, then the default. Unlike the event feed, the default here
 * is `mock` because the backend forge surface does not exist yet.
 */
export function resolveForgeMode(input: { search?: string; env?: string | undefined } = {}): ForgeMode {
  const q = new URLSearchParams(input.search ?? '').get('forge')
  for (const v of [q, input.env]) {
    if (v === 'mock' || v === 'http') return v
  }
  return 'mock'
}

export interface CreateForgeClientOptions {
  mode?: ForgeMode
  search?: string
  env?: string
  /** Base URL override for http mode (else VITE_FORGE_BASE_URL, else default). */
  baseUrl?: string
  http?: Omit<HttpForgeClientOptions, 'baseUrl'>
}

export function createForgeClient(opts: CreateForgeClientOptions = {}): ForgeClient {
  const mode = opts.mode ?? resolveForgeMode({ search: opts.search, env: opts.env })
  if (mode === 'http') return new HttpForgeClient({ ...opts.http, baseUrl: opts.baseUrl })
  return createMockForgeClient()
}

/** Browser convenience: reads window.location.search and Vite env. */
export function createDefaultForgeClient(): ForgeClient {
  return createForgeClient({
    search: typeof window !== 'undefined' ? window.location.search : '',
    env: import.meta.env.VITE_FORGE_SOURCE as string | undefined,
    baseUrl: import.meta.env.VITE_FORGE_BASE_URL as string | undefined,
  })
}
