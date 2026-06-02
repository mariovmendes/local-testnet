import { useState, useEffect, useMemo } from 'react'
import SystemDiagram from './components/visualization/SystemDiagram'
import TransactionFlowPanel, {
  FlowMode,
} from './components/visualization/TransactionFlowPanel'
import TransactionPanel, {
  type TransactionPanelMode,
} from './components/transactions/TransactionPanel'
import { statusOf, useTransactionStore } from './stores/transactionStore'
import { CHAIN_A_ID, CHAIN_A_EXPLORER, CHAIN_B_EXPLORER, getProvider } from './api/rollup'
import { fetchServices, indexById, type ServiceStatus } from './api/health'
import { BUNDLER_TEST_AVAILABLE, FLASHBLOCKS_ENABLED } from './config/chains'

function StatusChip({ status, label }: { status: ServiceStatus; label: string }) {
  const palette: Record<ServiceStatus, { dot: string; pulse: boolean }> = {
    up: { dot: 'bg-cyan', pulse: true },
    starting: { dot: 'bg-yellow-400', pulse: true },
    down: { dot: 'bg-error', pulse: false },
    missing: { dot: 'bg-border-bright', pulse: false },
  }
  const { dot, pulse } = palette[status]
  return (
    <div className="flex items-center gap-1.5" title={`${label}: ${status}`}>
      <span className={`w-1.5 h-1.5 rounded-full ${dot} ${pulse ? 'indicator-active' : ''}`} />
      <span className="text-text-secondary text-[10px] font-display tracking-widest uppercase">
        {label}
      </span>
    </div>
  )
}

type LogFilter = 'all' | 'pending' | 'committed' | 'aborted'

function App() {
  const [activeTab, setActiveTab] = useState<TransactionPanelMode>('mint')
  const [flowMode, setFlowMode] = useState<FlowMode | null>(null)
  const [diagramResetSignal, setDiagramResetSignal] = useState(0)
  const { transactions, currentStatus, setServices, clearTransactions } = useTransactionStore()
  const [copiedId, setCopiedId] = useState<string | null>(null)
  const [logFilter, setLogFilter] = useState<LogFilter>('all')

  useEffect(() => {
    const controller = new AbortController()
    let cancelled = false

    const poll = async () => {
      try {
        const services = await fetchServices(controller.signal)
        if (!cancelled) {
          setServices(indexById(services))
        }
      } catch (err) {
        if ((err as { name?: string }).name === 'AbortError') return
        // Reset rather than retain stale statuses: a health-api outage
        // must not mask sibling failures.
        if (!cancelled) {
          setServices({})
        }
      }
    }

    poll()
    const interval = setInterval(poll, 5000)
    return () => {
      cancelled = true
      controller.abort()
      clearInterval(interval)
    }
  }, [setServices])

  const services = currentStatus.services
  const headerStatuses = useMemo(
    () => ({
      chainA: statusOf(services, 'op-reth-a'),
      chainB: statusOf(services, 'op-reth-b'),
      sidecarA: statusOf(services, 'sidecar-a'),
      sidecarB: statusOf(services, 'sidecar-b'),
      altDaA: statusOf(services, 'op-alt-da-a'),
      altDaB: statusOf(services, 'op-alt-da-b'),
      opSuccinctA: statusOf(services, 'op-succinct-a'),
      opSuccinctB: statusOf(services, 'op-succinct-b'),
    }),
    [services],
  )

  const altDaEnabled = headerStatuses.altDaA !== 'missing' || headerStatuses.altDaB !== 'missing'
  const opSuccinctEnabled =
    headerStatuses.opSuccinctA !== 'missing' || headerStatuses.opSuccinctB !== 'missing'

  const { updateTransaction } = useTransactionStore()
  useEffect(() => {
    const pending = transactions.filter(
      (tx) => tx.status !== 'committed' && tx.status !== 'aborted'
    )
    if (pending.length === 0) return

    const check = async () => {
      await Promise.all(
        pending.map(async (tx) => {
          try {
            const provider = getProvider(tx.chainId === CHAIN_A_ID ? 'A' : 'B')
            const receipt = await provider.getTransactionReceipt(tx.instanceId)
            if (receipt) {
              updateTransaction(tx.instanceId, {
                status: receipt.status === 1 ? 'committed' : 'aborted',
                decision: receipt.status === 1,
                decidedAt: new Date(),
              })
            }
          } catch {
            // ignore - tx may not be on-chain yet
          }
        })
      )
    }

    const interval = setInterval(check, 2000)
    return () => clearInterval(interval)
  }, [transactions, updateTransaction])

  const handleCopyId = (id: string) => {
    navigator.clipboard.writeText(id)
    setCopiedId(id)
    setTimeout(() => setCopiedId(null), 1500)
  }

  const formatDuration = (createdAt: Date, decidedAt?: Date) => {
    if (!decidedAt) return null
    const ms = decidedAt.getTime() - createdAt.getTime()
    return ms < 1000 ? `${ms}ms` : `${(ms / 1000).toFixed(1)}s`
  }

  // Explorer helpers — point to Otterscan (same URL scheme as Blockscout)
  const explorerBase = (chainId: number) =>
    chainId === CHAIN_A_ID ? CHAIN_A_EXPLORER : CHAIN_B_EXPLORER

  const explorerTxUrl     = (chainId: number, hash: string)    => `${explorerBase(chainId)}/tx/${hash}`
  const explorerAddrUrl   = (chainId: number, address: string) => `${explorerBase(chainId)}/address/${address}`
  const explorerBlockUrl  = (chainId: number, block: number)   => `${explorerBase(chainId)}/block/${block}`

  // Legacy alias kept for any existing call-sites
  const getBlockscoutUrl = (chainId: number, txHash: string) => explorerTxUrl(chainId, txHash)

  const statusColor = (status: string) => {
    if (status === 'committed') return 'text-cyan'
    if (status === 'aborted') return 'text-error'
    return 'text-yellow-400'
  }

  const statusDot = (status: string) => {
    if (status === 'committed') return 'bg-cyan'
    if (status === 'aborted') return 'bg-error'
    return 'bg-yellow-400'
  }

  return (
    <div className="scanline-overlay min-h-screen text-text-primary flex flex-col">
      {/* ── Header ── */}
      <header className="border-b border-border bg-bg-card flex-none">
        <div className="max-w-[1440px] mx-auto px-6 py-3 flex items-center justify-between gap-6">
          {/* Left: brand */}
          <div className="flex items-center gap-4">
            <img
              src="https://framerusercontent.com/images/Fb2oWhF4xWeQVhnTEkAGcHvKrc.png?width=4182&height=1547"
              alt="Ethera Labs"
              className="h-5 w-auto opacity-80"
              style={{ filter: 'brightness(0) invert(1)' }}
            />
            <div className="w-px h-4 bg-border-bright" />
            <span className="font-display text-[11px] tracking-[0.3em] uppercase text-text-secondary">
              Local-Testnet
            </span>
            <div className="w-px h-4 bg-border-bright" />
            <span className="font-display text-[11px] tracking-[0.3em] uppercase text-text-primary">
              Ethera Labs Console
            </span>
          </div>

          {/* Right: live status */}
          <div className="flex items-center gap-4 flex-wrap justify-end">
            <StatusChip status={headerStatuses.chainA} label="Chain A" />
            <StatusChip status={headerStatuses.chainB} label="Chain B" />
            <StatusChip status={headerStatuses.sidecarA} label="Sidecar A" />
            <StatusChip status={headerStatuses.sidecarB} label="Sidecar B" />
            {altDaEnabled && (
              <>
                <StatusChip status={headerStatuses.altDaA} label="AltDA A" />
                <StatusChip status={headerStatuses.altDaB} label="AltDA B" />
              </>
            )}
            {opSuccinctEnabled && (
              <>
                <StatusChip status={headerStatuses.opSuccinctA} label="op-succinct A" />
                <StatusChip status={headerStatuses.opSuccinctB} label="op-succinct B" />
              </>
            )}
            {FLASHBLOCKS_ENABLED && (
              <div className="hidden sm:flex items-center gap-1.5 border border-amber/40 px-2 py-0.5 bg-amber/5">
                <span className="w-1.5 h-1.5 rounded-full bg-amber indicator-active" />
                <span className="font-display text-[10px] tracking-widest uppercase text-amber">
                  Flashblocks Active
                </span>
              </div>
            )}
          </div>
        </div>
      </header>

      {/* ── Main ── */}
      <main className="flex-1 max-w-[1440px] mx-auto w-full px-6 py-6 grid grid-cols-1 xl:grid-cols-[1fr_420px] gap-6">

        {/* Left column: System Diagram + Flow Panel */}
        <div className="flex flex-col gap-6">
          {/* Architecture diagram card */}
          <div className="cb bg-bg-card border border-border flex flex-col h-[580px]">
            <div className="border-b border-border px-5 py-3 flex items-center justify-between flex-none">
              <div className="flex items-center gap-3">
                <span className="font-display text-[10px] tracking-[0.3em] uppercase text-amber">
                  System Architecture
                </span>
                <span className="text-border-bright">·</span>
                <span className="text-[10px] text-text-secondary font-mono">
                  {currentStatus.step.replace(/_/g, ' ')}
                </span>
              </div>
              <button
                onClick={() => setDiagramResetSignal((n) => n + 1)}
                className="text-[10px] font-display tracking-widest uppercase transition-colors text-text-secondary hover:text-amber"
              >
                Reset
              </button>
            </div>

            <div className="flex-1 relative">
              <SystemDiagram
                currentStatus={currentStatus}
                onSelectFlow={setFlowMode}
                selectedFlow={flowMode}
                resetSignal={diagramResetSignal}
              />
            </div>
          </div>

          {/* Flow documentation panel */}
          <div className="cb bg-bg-card border border-border flex-none">
            <TransactionFlowPanel
              mode={flowMode}
              onSelect={setFlowMode}
              onClear={() => setFlowMode(null)}
            />
          </div>
        </div>

        {/* Right column: Controls + Log */}
        <div className="flex flex-col gap-6 min-w-0">
          <div className="cb bg-bg-card border border-border flex flex-col h-[580px]">
            <div className="border-b border-border flex-none">
              <div className="flex">
                <button
                  onClick={() => setActiveTab('mint')}
                  className={`flex-1 px-3 py-3 font-display text-[10px] tracking-[0.2em] uppercase transition-all border-b-2 ${
                    activeTab === 'mint' ? 'border-cyan text-cyan bg-cyan/5' : 'border-transparent text-text-secondary hover:text-text-primary'
                  }`}
                >
                  Mint
                </button>
                <button
                  onClick={() => setActiveTab('bridge')}
                  className={`flex-1 px-3 py-3 font-display text-[10px] tracking-[0.2em] uppercase transition-all border-b-2 ${
                    activeTab === 'bridge' ? 'border-amber text-amber bg-amber/5' : 'border-transparent text-text-secondary hover:text-text-primary'
                  }`}
                >
                  Bridge XT
                </button>
                <button
                  onClick={() => setActiveTab('atomicity')}
                  className={`flex-1 px-3 py-3 font-display text-[10px] tracking-[0.2em] uppercase transition-all border-b-2 ${
                    activeTab === 'atomicity' ? 'border-warning text-warning bg-warning/5' : 'border-transparent text-text-secondary hover:text-text-primary'
                  }`}
                >
                  Scenarios
                </button>
                <button
                  onClick={() => setActiveTab('stress')}
                  className={`flex-1 px-3 py-3 font-display text-[10px] tracking-[0.2em] uppercase transition-all border-b-2 ${
                    activeTab === 'stress' ? 'border-error text-error bg-error/5' : 'border-transparent text-text-secondary hover:text-text-primary'
                  }`}
                >
                  Stress
                </button>
                {BUNDLER_TEST_AVAILABLE && (
                  <button
                    onClick={() => setActiveTab('bundler')}
                    className={`flex-1 px-3 py-3 font-display text-[10px] tracking-[0.2em] uppercase transition-all border-b-2 ${
                      activeTab === 'bundler'
                        ? 'border-cyan text-cyan bg-cyan/5'
                        : 'border-transparent text-text-secondary hover:text-text-primary'
                    }`}
                  >
                    Bundler
                  </button>
                )}
              </div>
            </div>
            <div className="p-5 flex-1 min-h-0 overflow-y-auto">
              <TransactionPanel mode={activeTab} onSelectFlow={setFlowMode} />
            </div>
          </div>

          {/* Transaction log */}
          {transactions.length > 0 && (() => {
            const counts = transactions.reduce(
              (acc, tx) => {
                if (tx.status === 'committed') acc.committed += 1
                else if (tx.status === 'aborted') acc.aborted += 1
                else acc.pending += 1
                return acc
              },
              { committed: 0, aborted: 0, pending: 0 }
            )
            const filtered = transactions.filter((tx) => {
              if (logFilter === 'all') return true
              if (logFilter === 'pending') {
                return tx.status !== 'committed' && tx.status !== 'aborted'
              }
              return tx.status === logFilter
            })
            const filters: { id: LogFilter; label: string; count: number; tone: string }[] = [
              { id: 'all', label: 'All', count: transactions.length, tone: 'text-text-secondary' },
              { id: 'pending', label: 'Pending', count: counts.pending, tone: 'text-yellow-400' },
              { id: 'committed', label: 'Commit', count: counts.committed, tone: 'text-cyan' },
              { id: 'aborted', label: 'Abort', count: counts.aborted, tone: 'text-error' },
            ]
            return (
            <div className="cb bg-bg-card border border-border">
              <div className="border-b border-border px-4 py-2 flex items-center justify-between gap-3">
                <div className="flex items-center gap-1">
                  {filters.map((f) => {
                    const active = logFilter === f.id
                    return (
                      <button
                        key={f.id}
                        onClick={() => setLogFilter(f.id)}
                        className={`px-2 py-1 font-display text-[9px] tracking-[0.2em] uppercase border transition-colors ${
                          active
                            ? `border-amber bg-amber/5 text-amber`
                            : `border-transparent ${f.tone} hover:border-border-bright`
                        }`}
                      >
                        {f.label} <span className="text-text-dim ml-0.5">{f.count}</span>
                      </button>
                    )
                  })}
                </div>
                <button
                  onClick={clearTransactions}
                  className="px-2 py-1 font-display text-[9px] tracking-[0.2em] uppercase border border-transparent text-text-secondary hover:border-error hover:text-error transition-colors"
                  title="Clear log"
                >
                  Clear
                </button>
              </div>
              <div className="divide-y divide-border max-h-[360px] overflow-y-auto">
                {filtered.length === 0 && (
                  <div className="px-4 py-6 text-center text-[10px] text-text-dim font-mono">
                    No {logFilter === 'all' ? '' : logFilter + ' '}entries
                  </div>
                )}
                {filtered.map((tx) => (
                  <div
                    key={tx.instanceId}
                    className="px-4 py-3 flex items-start justify-between gap-3 hover:bg-bg-elevated/50 transition-colors animate-fade-slide-in"
                  >
                    <div className="min-w-0 flex-1">
                      <div className="flex items-center gap-2 mb-0.5">
                        <span
                          className={`w-1.5 h-1.5 rounded-full flex-none ${statusDot(tx.status)} ${
                            tx.status !== 'committed' && tx.status !== 'aborted'
                              ? 'indicator-active'
                              : ''
                          }`}
                        />
                        <span className="text-[10px] font-display tracking-widest uppercase text-text-secondary">
                          {tx.type}
                        </span>
                        <span className="text-[9px] font-display tracking-widest uppercase text-text-dim">
                          chain {tx.chainId === CHAIN_A_ID ? 'A' : 'B'}
                        </span>
                      </div>
                      <div className="flex items-center gap-1.5">
                        <a
                          href={explorerTxUrl(tx.chainId, tx.instanceId)}
                          target="_blank"
                          rel="noopener noreferrer"
                          className="font-mono text-[11px] text-text-secondary hover:text-amber transition-colors"
                          title={`View in Otterscan: ${tx.instanceId}`}
                        >
                          {tx.instanceId.slice(0, 12)}…{tx.instanceId.slice(-6)} ↗
                        </a>
                        <button
                          onClick={() => handleCopyId(tx.instanceId)}
                          className="text-text-dim hover:text-amber transition-colors flex-none"
                          title="Copy full ID"
                        >
                          {copiedId === tx.instanceId ? (
                            <svg width="11" height="11" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2.5" strokeLinecap="square">
                              <path d="M20 6L9 17l-5-5" />
                            </svg>
                          ) : (
                            <svg width="11" height="11" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="square">
                              <rect x="9" y="9" width="13" height="13" />
                              <path d="M5 15H4a2 2 0 0 1-2-2V4a2 2 0 0 1 2-2h9a2 2 0 0 1 2 2v1" />
                            </svg>
                          )}
                        </button>
                      </div>
                    </div>
                    <div className="flex flex-col items-end gap-1 flex-none">
                      <span className={`text-[10px] font-display tracking-widest uppercase ${statusColor(tx.status)}`}>
                        {tx.status}
                      </span>
                      {(() => {
                        const dur = formatDuration(tx.createdAt, tx.decidedAt)
                        return dur ? (
                          <span className="font-mono text-[10px] text-text-dim">{dur}</span>
                        ) : null
                      })()}
                    </div>
                  </div>
                ))}
              </div>
            </div>
            )
          })()}
        </div>
      </main>
    </div>
  )
}

export default App
