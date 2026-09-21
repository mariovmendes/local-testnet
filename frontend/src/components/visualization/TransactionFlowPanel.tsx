import { EL_CLIENT_LABEL } from '../../config/chains'

export type FlowMode = 'normal' | 'xt'

interface FlowStep {
  title: string
  detail: string
  subDetails?: string[]
}

interface FlowContent {
  title: string
  subtitle: string
  tags: string[]
  steps: FlowStep[]
  timing?: { title: string; items: string[] }
  note?: string
}

const flowContent: Record<FlowMode, FlowContent> = {
  normal: {
    title: 'Normal Transaction Flow',
    subtitle: 'Flashblocks + rollup-boost path',
    tags: ['engine_FCU', 'engine_getPayload', 'mempool', 'flashblocks'],
    steps: [
      {
        title: 'Submit to builder RPC',
        detail: 'User sends tx to op-rbuilder. The tx enters the builder transaction pool ordered by gas price/priority.',
      },
      {
        title: 'Start block build (FCU)',
        detail: `op-node calls engine_forkchoiceUpdated on rollup-boost, which forwards to both ${EL_CLIENT_LABEL} (proposer) and op-rbuilder (builder).`,
        subDetails: ['op-rbuilder starts building flashblocks immediately', 'Fallback block (deposits only) is built first'],
      },
      {
        title: 'Flashblock building loop',
        detail: 'Every 250ms (configurable), op-rbuilder builds a new flashblock chunk. Each flashblock pulls txs from the mempool using best_transactions_with_attributes().',
        subDetails: ['Txs ordered by priority (tip/gas price)', 'Executed until gas/DA limits reached', 'Published via WebSocket to subscribers'],
      },
      {
        title: 'Assemble final payload',
        detail: `op-node calls engine_getPayload; rollup-boost fetches the builder payload (all flashblocks merged) and the proposer fallback payload from ${EL_CLIENT_LABEL}.`,
      },
      {
        title: 'Validate builder payload',
        detail: `rollup-boost validates the builder payload by calling ${EL_CLIENT_LABEL} engine_newPayload. This ensures the payload is valid before returning it.`,
      },
      {
        title: 'Fallback on invalid',
        detail: `If validation fails, rollup-boost returns the ${EL_CLIENT_LABEL} payload instead. The builder payload is discarded.`,
      },
      {
        title: 'Finalize chain head',
        detail: 'op-node submits engine_newPayload + engine_FCU (no attrs) to advance the canonical head.',
      },
    ],
    timing: {
      title: 'Flashblock Timeline (2s block, 250ms interval)',
      items: [
        '0ms: Fallback block (deposits only)',
        '250ms: Flashblock 1 → pull mempool txs',
        '500ms: Flashblock 2 → pull mempool txs',
        '750ms: Flashblock 3 → pull mempool txs',
        '...continues every 250ms...',
        '2000ms: Block finalized (state root calculated)',
      ],
    },
    note: `If flashblocks are disabled, normal txs go directly to ${EL_CLIENT_LABEL} mempool instead.`,
  },
  xt: {
    title: 'Cross-Chain XT Flow',
    subtitle: 'Sidecar coordination + 2PC + builder push',
    tags: ['POST /xt', 'ethera_submitXt', 'CIRC', '2PC', 'ethera_releaseXt', 'POST /ethera/confirm'],
    steps: [
      {
        title: 'Submit XT bundle',
        detail: 'User submits a cross-chain transaction to the sidecar via POST /xt. The sidecar fingerprints, deduplicates, and stores it as a PendingXt.',
        subDetails: ['XT contains txs for multiple chains: {chainA: tx1, chainB: tx2}'],
      },
      {
        title: 'Lock builder slot',
        detail: 'Sidecar pushes ethera_submitXt(instance_id, order, transactions) to the local op-rbuilder. The builder reserves a slot for this XT until the sidecar releases or aborts it.',
        subDetails: [
          'JSON-RPC method: ethera_submitXt',
          'order = (period_id, sequence_number) from the SBCP period',
          'No polling - sidecar drives the lifecycle',
        ],
      },
      {
        title: 'Simulate + mailbox trace',
        detail: 'Sidecar simulates each tx with debug_traceCall against the rollup RPC, applying per-chain state overlays so sequential XTs observe each other. Traces UniversalBridgeMailbox writeMessage / readMessage calls.',
        subDetails: [
          'readMessage() → creates CrossRollupDependency (needs data from another chain)',
          'writeMessage() → creates CrossRollupMessage (sends data to another chain)',
        ],
      },
      {
        title: 'CIRC message exchange',
        detail: 'Sidecars exchange CIRC mailbox messages over POST /mailbox to satisfy each other\'s readMessage dependencies, re-simulating with state overrides until all dependencies resolve.',
        subDetails: [
          'Peer transport: HTTP POST /mailbox',
          'Loops until every dependency is fulfilled or the CIRC timer expires',
        ],
      },
      {
        title: 'Vote + 2PC decision',
        detail: 'Each sidecar votes commit or abort. In coordinated mode votes go to the Shared Publisher; in standalone mode votes flow peer-to-peer via POST /xt/vote. A single abort vote → ABORT; unanimous commits → COMMIT.',
      },
      {
        title: 'Push commit / abort to builder',
        detail: 'On COMMIT: sidecar builds putInbox transactions and pushes ethera_releaseXt(instance_id, putInboxTxs). On ABORT: sidecar pushes ethera_abortXt(instance_id) so the builder releases the slot.',
        subDetails: [
          'putInbox txs use a deferred nonce manager to stay sequential under concurrency',
          'Builder includes the locked XT in its next flashblock without further coordination',
        ],
      },
      {
        title: 'Builder confirms inclusion',
        detail: 'After the XT lands on chain, the builder calls POST /ethera/confirm with the included instance IDs. The sidecar marks the PendingXt as confirmed, and GET /xt/:id observers see the final status.',
      },
    ],
    timing: {
      title: 'Push Protocol Timeline',
      items: [
        'POST /xt → sidecar accepts and persists the XT',
        'sidecar → builder: ethera_submitXt (slot locked)',
        'sidecar simulates, exchanges CIRC over POST /mailbox',
        'sidecars cast votes (SP or P2P)',
        '2PC decides → sidecar pushes ethera_releaseXt or ethera_abortXt',
        'builder includes XT, then calls POST /ethera/confirm',
      ],
    },
    note: 'The sidecar drives the builder via JSON-RPC pushes (ethera_submitXt / releaseXt / abortXt). Inclusion is reported back over POST /ethera/confirm - there is no polling or hold-and-deliver loop.',
  },
}

interface TransactionFlowPanelProps {
  mode: FlowMode | null
  onSelect: (mode: FlowMode) => void
  onClear: () => void
}

export default function TransactionFlowPanel({ mode, onSelect, onClear }: TransactionFlowPanelProps) {
  const activeFlow = mode ? flowContent[mode] : null
  const accentColor = mode === 'xt' ? 'amber' : 'cyan'

  return (
    <div className="p-5">
      {/* Selector */}
      <div className="flex items-center justify-between mb-4">
        <span className="font-display text-[10px] tracking-[0.3em] uppercase text-text-secondary">
          Protocol Flow
        </span>
        <div className="flex items-center gap-2">
          <button
            onClick={() => onSelect('normal')}
            className={`px-2.5 py-1 font-display text-[9px] tracking-widest uppercase border transition-all ${
              mode === 'normal'
                ? 'border-cyan text-cyan bg-cyan/5'
                : 'border-border text-text-dim hover:border-border-bright hover:text-text-secondary'
            }`}
          >
            Normal TX
          </button>
          <button
            onClick={() => onSelect('xt')}
            className={`px-2.5 py-1 font-display text-[9px] tracking-widest uppercase border transition-all ${
              mode === 'xt'
                ? 'border-amber text-amber bg-amber/5'
                : 'border-border text-text-dim hover:border-border-bright hover:text-text-secondary'
            }`}
          >
            Submit XT
          </button>
          <button
            onClick={onClear}
            disabled={!mode}
            className="text-[9px] font-display tracking-widest uppercase transition-colors disabled:text-border disabled:cursor-not-allowed text-text-dim hover:text-text-secondary"
          >
            Clear
          </button>
        </div>
      </div>

      {activeFlow ? (
        <div className="space-y-4">
          {/* Title */}
          <div>
            <h4 className={`font-display text-sm tracking-wider ${accentColor === 'amber' ? 'text-amber' : 'text-cyan'}`}>
              {activeFlow.title}
            </h4>
            <p className="text-[10px] text-text-dim font-mono mt-0.5">{activeFlow.subtitle}</p>
          </div>

          {/* Tags */}
          <div className="flex flex-wrap gap-1.5">
            {activeFlow.tags.map((tag) => (
              <span
                key={tag}
                className="px-2 py-0.5 text-[9px] font-mono bg-bg border border-border text-text-dim"
              >
                {tag}
              </span>
            ))}
          </div>

          {/* Steps */}
          <div className="space-y-3 max-h-[320px] overflow-y-auto pr-1">
            {activeFlow.steps.map((step, index) => (
              <div key={step.title} className="flex gap-3">
                <div
                  className={`flex h-5 w-5 shrink-0 items-center justify-center border text-[9px] font-mono mt-0.5 ${
                    accentColor === 'amber'
                      ? 'border-amber/40 text-amber/70'
                      : 'border-cyan/40 text-cyan/70'
                  }`}
                >
                  {index + 1}
                </div>
                <div className="min-w-0">
                  <p className="text-[11px] font-display tracking-wide text-text-primary">{step.title}</p>
                  <p className="text-[10px] text-text-secondary font-mono mt-0.5 leading-relaxed">{step.detail}</p>
                  {step.subDetails && step.subDetails.length > 0 && (
                    <ul className="mt-1.5 space-y-0.5">
                      {step.subDetails.map((sub, i) => (
                        <li key={i} className="text-[10px] text-text-dim font-mono pl-3 border-l border-border">
                          {sub}
                        </li>
                      ))}
                    </ul>
                  )}
                </div>
              </div>
            ))}
          </div>

          {/* Timing */}
          {activeFlow.timing && (
            <div className="border border-border bg-bg p-3">
              <p className="font-display text-[9px] tracking-widest uppercase text-text-secondary mb-2">
                {activeFlow.timing.title}
              </p>
              <div className="space-y-1">
                {activeFlow.timing.items.map((item, i) => (
                  <p key={i} className="text-[10px] text-text-dim font-mono">{item}</p>
                ))}
              </div>
            </div>
          )}

          {/* Note */}
          {activeFlow.note && (
            <div className="border-l-2 border-border-bright pl-3 py-1">
              <p className="text-[10px] text-text-dim font-mono leading-relaxed">{activeFlow.note}</p>
            </div>
          )}
        </div>
      ) : (
        <div className="border border-dashed border-border px-4 py-6 text-center">
          <p className="text-[10px] text-text-dim font-mono">
            Select a flow above or click edges in the diagram
          </p>
        </div>
      )}
    </div>
  )
}
