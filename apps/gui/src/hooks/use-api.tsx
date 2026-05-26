import { createApiContext } from '@hollis-labs/sysop-ui/api'
import { TorqueApiClient } from '@/lib/api'

const API_BASE_URL = import.meta.env.VITE_API_BASE_URL as string | undefined

/** The default Torque API client; tests can override via `<ApiProvider client>`. */
export const apiClient = new TorqueApiClient(API_BASE_URL)

export const { ApiProvider, useApi } = createApiContext(apiClient)
