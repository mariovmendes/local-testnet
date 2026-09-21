import {
  CHAIN_A_BUILDER_RPC,
  CHAIN_A_ID,
  CHAIN_A_OP_RETH_RPC,
  CHAIN_B_BUILDER_RPC,
  CHAIN_B_ID,
  CHAIN_B_OP_RETH_RPC,
  HEALTH_API_ENABLED,
  HEALTH_API_URL,
  SIDECAR_A_URL,
  SIDECAR_B_URL,
} from '../config/chains'

// Keep in sync with the catalogue in cmd/localnet-health/main.go.
export type ServiceKind =
  | 'publisher'
  | 'op-reth'
  | 'op-node'
  | 'op-batcher'
  | 'op-proposer'
  | 'rollup-boost'
  | 'op-rbuilder'
  | 'sidecar'
  | 'op-alt-da'
  | 'op-succinct'
  | 'op-succinct-postgres'
  | 'frontend'

// "missing" = optional feature disabled (container absent); "down" =
// container exists but is not running / unhealthy.
export type ServiceStatus = 'up' | 'starting' | 'down' | 'missing'

export interface Service {
  id: string
  name: string
  kind: ServiceKind
  host_port?: number
  status: ServiceStatus
}

interface ServicesResponse {
  services: Service[]
}

function joinPath(baseUrl: string, path: string): string {
  return `${baseUrl.replace(/\/+$/, '')}${path}`
}

async function probeRpc(
  url: string,
  expectedChainId: number,
  signal?: AbortSignal,
): Promise<ServiceStatus> {
  try {
    const response = await fetch(url, {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({
        jsonrpc: '2.0',
        id: 1,
        method: 'eth_chainId',
        params: [],
      }),
      signal,
    })
    if (!response.ok) return 'down'
    const data = (await response.json()) as { result?: string }
    return Number.parseInt(data.result ?? '', 16) === expectedChainId ? 'up' : 'down'
  } catch (err) {
    if ((err as { name?: string }).name === 'AbortError') throw err
    return 'down'
  }
}

async function probeHttp(url: string, signal?: AbortSignal): Promise<ServiceStatus> {
  try {
    const response = await fetch(joinPath(url, '/health'), {
      headers: { Accept: 'application/json' },
      signal,
    })
    return response.ok ? 'up' : 'down'
  } catch (err) {
    if ((err as { name?: string }).name === 'AbortError') throw err
    return 'down'
  }
}

async function fetchConfiguredServices(signal?: AbortSignal): Promise<Service[]> {
  const [
    chainA,
    chainB,
    builderA,
    builderB,
    sidecarA,
    sidecarB,
  ] = await Promise.all([
    probeRpc(CHAIN_A_OP_RETH_RPC, CHAIN_A_ID, signal),
    probeRpc(CHAIN_B_OP_RETH_RPC, CHAIN_B_ID, signal),
    probeRpc(CHAIN_A_BUILDER_RPC, CHAIN_A_ID, signal),
    probeRpc(CHAIN_B_BUILDER_RPC, CHAIN_B_ID, signal),
    probeHttp(SIDECAR_A_URL, signal),
    probeHttp(SIDECAR_B_URL, signal),
  ])

  return [
    {
      id: 'op-reth-a',
      name: 'op-reth A',
      kind: 'op-reth',
      status: chainA,
    },
    {
      id: 'op-reth-b',
      name: 'op-reth B',
      kind: 'op-reth',
      status: chainB,
    },
    {
      id: 'op-rbuilder-a',
      name: 'op-rbuilder A',
      kind: 'op-rbuilder',
      status: builderA,
    },
    {
      id: 'op-rbuilder-b',
      name: 'op-rbuilder B',
      kind: 'op-rbuilder',
      status: builderB,
    },
    {
      id: 'sidecar-a',
      name: 'Sidecar A',
      kind: 'sidecar',
      status: sidecarA,
    },
    {
      id: 'sidecar-b',
      name: 'Sidecar B',
      kind: 'sidecar',
      status: sidecarB,
    },
  ]
}

export async function fetchServices(
  signal?: AbortSignal,
  baseUrl: string = HEALTH_API_URL,
): Promise<Service[]> {
  if (!HEALTH_API_ENABLED && baseUrl === HEALTH_API_URL) {
    return fetchConfiguredServices(signal)
  }

  const response = await fetch(`${baseUrl}/api/services`, {
    method: 'GET',
    headers: { Accept: 'application/json' },
    signal,
  })
  if (!response.ok) {
    throw new Error(`health api ${response.status}`)
  }
  const data = (await response.json()) as ServicesResponse
  return data.services
}

export function indexById(services: Service[]): Record<string, Service> {
  const out: Record<string, Service> = {}
  for (const s of services) {
    out[s.id] = s
  }
  return out
}
