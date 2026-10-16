import { createContext } from 'react'
import type { ConsoleSeams } from './provider'
import type { ItemState } from '../seams/state-store'
import type { ConsoleStore } from './store'

export const StoreContext = createContext<ConsoleStore | null>(null)
export const SeamsContext = createContext<ConsoleSeams | null>(null)
export const ItemStateContext = createContext<Record<string, ItemState>>({})
