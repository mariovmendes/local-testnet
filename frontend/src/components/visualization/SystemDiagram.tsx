import { useCallback, useMemo, useState, useEffect, useRef } from 'react'
import type { MouseEvent } from 'react'
import ReactFlow, {
  Node,
  Edge,
  Background,
  Controls,
  NodeTypes,
  MarkerType,
  ReactFlowInstance,
  useReactFlow,
} from 'reactflow'
import 'reactflow/dist/style.css'
import { CurrentStatus, FlowStep, statusOf } from '../../stores/transactionStore'
import { EL_CLIENT_LABEL } from '../../config/chains'
import type { ServiceStatus } from '../../api/health'
import type { FlowMode } from './TransactionFlowPanel'
import RollupNode from './nodes/RollupNode'
import SidecarNode from './nodes/SidecarNode'
import BuilderNode from './nodes/BuilderNode'
import BoostNode from './nodes/BoostNode'
import OpNodeNode from './nodes/OpNodeNode'
import OpRethNode from './nodes/OpRethNode'
import PublisherNode from './nodes/PublisherNode'
import UserNode from './nodes/UserNode'
import BatcherNode from './nodes/BatcherNode'
import ProposerNode from './nodes/ProposerNode'
import AltDaNode from './nodes/AltDaNode'
import OpSuccinctNode from './nodes/OpSuccinctNode'

interface SystemDiagramProps {
  currentStatus: CurrentStatus
  onSelectFlow?: (mode: FlowMode) => void
  selectedFlow?: FlowMode | null
  resetSignal?: number
}

const nodeTypes: NodeTypes = {
  rollup: RollupNode,
  sidecar: SidecarNode,
  builder: BuilderNode,
  boost: BoostNode,
  opnode: OpNodeNode,
  'op-reth': OpRethNode,
  publisher: PublisherNode,
  user: UserNode,
  batcher: BatcherNode,
  proposer: ProposerNode,
  altda: AltDaNode,
  opsuccinct: OpSuccinctNode,
}

function getEdgeStatus(step: FlowStep, edgeId: string): 'idle' | 'active' | 'complete' {
  const activeEdges: Record<FlowStep, string[]> = {
    idle: [],
    submitting: [
      'user-sidecar-a',
      'user-sidecar-b',
      'publisher-sidecar-a',
      'publisher-sidecar-b',
    ],
    minting_a: ['op-node-a-boost-a', 'boost-a-builder-a'],
    minting_b: ['op-node-b-boost-b', 'boost-b-builder-b'],
    minting_both: [
      'op-node-a-boost-a',
      'boost-a-builder-a',
      'op-node-b-boost-b',
      'boost-b-builder-b',
    ],
    forward_to_peer: ['sidecar-a-sidecar-b'],
    lock_builder_a: ['sidecar-a-builder-a'],
    lock_builder_b: ['sidecar-b-builder-b'],
    simulating_a: ['sidecar-a-simulate-builder-a'],
    simulating_b: ['sidecar-b-simulate-builder-b'],
    circ_exchange: ['sidecar-a-sidecar-b'],
    voting: ['sidecar-a-sidecar-b', 'sidecar-a-publisher', 'sidecar-b-publisher'],
    decided: ['publisher-sidecar-a', 'publisher-sidecar-b'],
    delivering: ['sidecar-a-builder-a', 'sidecar-b-builder-b'],
    confirming: ['builder-a-sidecar-a', 'builder-b-sidecar-b'],
    complete: [],
  }

  if (activeEdges[step]?.includes(edgeId)) {
    return 'active'
  }
  return 'idle'
}

const EDGE_ACTIVE = '#00D4A8'
const EDGE_IDLE = '#505070'
const EDGE_MINT_ACTIVE = '#00D4A8'

function isPresent(status: ServiceStatus): boolean {
  return status !== 'missing'
}

// Re-fits the viewport once every node added on a node-count change has
// been measured. ReactFlow measures via ResizeObserver, so fitView called
// in the same tick frames a stale (zero-sized) bounding box.
function AutoFit({ nodeCount }: { nodeCount: number }) {
  const { fitView, getNodes } = useReactFlow()
  const lastFittedCount = useRef(-1)

  useEffect(() => {
    if (lastFittedCount.current === nodeCount) return

    let cancelled = false
    let attempts = 0
    const maxAttempts = 60 // ~1s at 60fps; give up rather than spin forever

    const tryFit = () => {
      if (cancelled) return
      const nodes = getNodes()
      const ready =
        nodes.length === nodeCount &&
        nodes.every((n) => (n.width ?? 0) > 0 && (n.height ?? 0) > 0)

      if (ready) {
        lastFittedCount.current = nodeCount
        fitView({ duration: 300 })
        return
      }
      if (++attempts >= maxAttempts) return
      requestAnimationFrame(tryFit)
    }

    requestAnimationFrame(tryFit)
    return () => {
      cancelled = true
    }
  }, [nodeCount, fitView, getNodes])

  return null
}

export default function SystemDiagram({
  currentStatus,
  onSelectFlow,
  selectedFlow,
  resetSignal,
}: SystemDiagramProps) {
  const { step, services } = currentStatus
  const highlightNormal = selectedFlow === 'normal'
  const highlightXt = selectedFlow === 'xt'
  const [isFullscreen, setIsFullscreen] = useState(false)
  const rfInstance = useRef<ReactFlowInstance | null>(null)

  useEffect(() => {
    if (!resetSignal) return
    rfInstance.current?.fitView({ duration: 300 })
  }, [resetSignal])

  useEffect(() => {
    const handleEscape = (e: KeyboardEvent) => {
      if (e.key === 'Escape' && isFullscreen) {
        setIsFullscreen(false)
      }
    }

    if (isFullscreen) {
      document.addEventListener('keydown', handleEscape)
      document.body.style.overflow = 'hidden'
    }

    return () => {
      document.removeEventListener('keydown', handleEscape)
      document.body.style.overflow = ''
    }
  }, [isFullscreen])

  const status = useMemo(() => {
    return {
      publisher: statusOf(services, 'publisher'),
      opRethA: statusOf(services, 'op-reth-a'),
      opRethB: statusOf(services, 'op-reth-b'),
      opNodeA: statusOf(services, 'op-node-a'),
      opNodeB: statusOf(services, 'op-node-b'),
      batcherA: statusOf(services, 'op-batcher-a'),
      batcherB: statusOf(services, 'op-batcher-b'),
      proposerA: statusOf(services, 'op-proposer-a'),
      proposerB: statusOf(services, 'op-proposer-b'),
      boostA: statusOf(services, 'rollup-boost-a'),
      boostB: statusOf(services, 'rollup-boost-b'),
      rbuilderA: statusOf(services, 'op-rbuilder-a'),
      rbuilderB: statusOf(services, 'op-rbuilder-b'),
      sidecarA: statusOf(services, 'sidecar-a'),
      sidecarB: statusOf(services, 'sidecar-b'),
      altDaA: statusOf(services, 'op-alt-da-a'),
      altDaB: statusOf(services, 'op-alt-da-b'),
      opSuccinctA: statusOf(services, 'op-succinct-a'),
      opSuccinctB: statusOf(services, 'op-succinct-b'),
      opSuccinctPg: statusOf(services, 'op-succinct-postgres'),
    }
  }, [services])

  const showFlashblocks = isPresent(status.boostA) || isPresent(status.rbuilderA)
  const showSidecars = isPresent(status.sidecarA) || isPresent(status.sidecarB)
  const showAltDa = isPresent(status.altDaA) || isPresent(status.altDaB)
  const showOpSuccinct =
    isPresent(status.opSuccinctA) || isPresent(status.opSuccinctB) || isPresent(status.opSuccinctPg)

  const nodes: Node[] = useMemo(() => {
    const ns: Node[] = []

    ns.push({
      id: 'user',
      type: 'user',
      position: { x: 400, y: -160 },
      data: { label: 'User' },
    })

    ns.push({
      id: 'op-node-a',
      type: 'opnode',
      position: { x: 50, y: 50 },
      data: { label: 'op-node A', port: 19545, active: status.opNodeA === 'up', status: status.opNodeA },
    })
    ns.push({
      id: 'op-node-b',
      type: 'opnode',
      position: { x: 750, y: 50 },
      data: { label: 'op-node B', port: 29545, active: status.opNodeB === 'up', status: status.opNodeB },
    })

    if (showFlashblocks) {
      ns.push({
        id: 'boost-a',
        type: 'boost',
        position: { x: 50, y: 170 },
        data: { label: 'rollup-boost A', port: 17551, active: status.boostA === 'up', status: status.boostA },
      })
      ns.push({
        id: 'boost-b',
        type: 'boost',
        position: { x: 750, y: 170 },
        data: { label: 'rollup-boost B', port: 27551, active: status.boostB === 'up', status: status.boostB },
      })

      ns.push({
        id: 'builder-a',
        type: 'builder',
        position: { x: 50, y: 290 },
        data: {
          label: 'op-rbuilder A',
          port: 17545,
          locked: step === 'lock_builder_a' || step === 'delivering',
          status: status.rbuilderA,
        },
      })
      ns.push({
        id: 'builder-b',
        type: 'builder',
        position: { x: 750, y: 290 },
        data: {
          label: 'op-rbuilder B',
          port: 27545,
          locked: step === 'lock_builder_b' || step === 'delivering',
          status: status.rbuilderB,
        },
      })
    }

    if (showSidecars) {
      ns.push({
        id: 'sidecar-a',
        type: 'sidecar',
        position: { x: 250, y: 410 },
        data: {
          label: 'Sidecar A',
          port: 17090,
          active: status.sidecarA === 'up',
          processing: ['simulating_a', 'voting'].includes(step),
          status: status.sidecarA,
        },
      })
      ns.push({
        id: 'sidecar-b',
        type: 'sidecar',
        position: { x: 550, y: 410 },
        data: {
          label: 'Sidecar B',
          port: 27090,
          active: status.sidecarB === 'up',
          processing: ['simulating_b', 'voting'].includes(step),
          status: status.sidecarB,
        },
      })
    }

    ns.push({
      id: 'publisher',
      type: 'publisher',
      position: { x: 400, y: 290 },
      data: {
        label: 'Publisher',
        port: 8080,
        active: status.publisher === 'up',
        coordinating: step === 'voting',
        status: status.publisher,
      },
    })

    ns.push({
      id: 'op-reth-a',
      type: 'op-reth',
      position: { x: 50, y: 530 },
      data: { label: `${EL_CLIENT_LABEL} A`, port: 18545, status: status.opRethA },
    })
    ns.push({
      id: 'op-reth-b',
      type: 'op-reth',
      position: { x: 750, y: 530 },
      data: { label: `${EL_CLIENT_LABEL} B`, port: 28545, status: status.opRethB },
    })

    ns.push({
      id: 'op-batcher-a',
      type: 'batcher',
      position: { x: 50, y: 650 },
      data: { label: 'op-batcher A', port: 18548, status: status.batcherA },
    })
    ns.push({
      id: 'op-batcher-b',
      type: 'batcher',
      position: { x: 750, y: 650 },
      data: { label: 'op-batcher B', port: 28548, status: status.batcherB },
    })
    ns.push({
      id: 'op-proposer-a',
      type: 'proposer',
      position: { x: 200, y: 650 },
      data: { label: 'op-proposer A', port: 18560, status: status.proposerA },
    })
    ns.push({
      id: 'op-proposer-b',
      type: 'proposer',
      position: { x: 600, y: 650 },
      data: { label: 'op-proposer B', port: 28560, status: status.proposerB },
    })

    if (showAltDa) {
      ns.push({
        id: 'op-alt-da-a',
        type: 'altda',
        position: { x: -150, y: 650 },
        data: { label: 'op-alt-da A', port: 3100, status: status.altDaA },
      })
      ns.push({
        id: 'op-alt-da-b',
        type: 'altda',
        position: { x: 900, y: 650 },
        data: { label: 'op-alt-da B', port: 3101, status: status.altDaB },
      })
    }

    if (showOpSuccinct) {
      ns.push({
        id: 'op-succinct-a',
        type: 'opsuccinct',
        position: { x: -150, y: 780 },
        data: { label: 'op-succinct A', port: 18082, variant: 'prover', status: status.opSuccinctA },
      })
      ns.push({
        id: 'op-succinct-b',
        type: 'opsuccinct',
        position: { x: 900, y: 780 },
        data: { label: 'op-succinct B', port: 28082, variant: 'prover', status: status.opSuccinctB },
      })
      ns.push({
        id: 'op-succinct-postgres',
        type: 'opsuccinct',
        position: { x: 400, y: 800 },
        data: { label: 'op-succinct Postgres', variant: 'postgres', status: status.opSuccinctPg },
      })
    }

    return ns
  }, [status, step, showFlashblocks, showSidecars, showAltDa, showOpSuccinct])

  const LBG = { fill: 'transparent' } as const
  const LSTYLE = (_activeColor: string, idleColor = '#9090B0') =>
    ({ fontSize: 9, fontFamily: '"IBM Plex Mono", monospace', fill: idleColor } as const)
  const LSTYLE_ACTIVE = (color: string) =>
    ({ fontSize: 9, fontFamily: '"IBM Plex Mono", monospace', fill: color } as const)

  const edges: Edge[] = useMemo(() => {
    const es: Edge[] = []

    if (showSidecars) {
      es.push({
        id: 'user-sidecar-a',
        source: 'user',
        target: 'sidecar-a',
        animated: getEdgeStatus(step, 'user-sidecar-a') === 'active',
        label: 'submit XT',
        labelStyle:
          getEdgeStatus(step, 'user-sidecar-a') === 'active' || highlightXt
            ? LSTYLE_ACTIVE(EDGE_ACTIVE)
            : LSTYLE(EDGE_ACTIVE),
        labelBgStyle: LBG,
        style: {
          stroke: getEdgeStatus(step, 'user-sidecar-a') === 'active' || highlightXt ? EDGE_ACTIVE : EDGE_IDLE,
          strokeWidth: 1.5,
          strokeDasharray: getEdgeStatus(step, 'user-sidecar-a') === 'active' || highlightXt ? '0' : '5,5',
        },
        markerEnd: {
          type: MarkerType.ArrowClosed,
          color: getEdgeStatus(step, 'user-sidecar-a') === 'active' || highlightXt ? EDGE_ACTIVE : EDGE_IDLE,
        },
      })
      es.push({
        id: 'user-sidecar-b',
        source: 'user',
        target: 'sidecar-b',
        animated: getEdgeStatus(step, 'user-sidecar-b') === 'active',
        label: 'submit XT',
        labelStyle:
          getEdgeStatus(step, 'user-sidecar-b') === 'active' || highlightXt
            ? LSTYLE_ACTIVE(EDGE_ACTIVE)
            : LSTYLE(EDGE_ACTIVE),
        labelBgStyle: LBG,
        style: {
          stroke: getEdgeStatus(step, 'user-sidecar-b') === 'active' || highlightXt ? EDGE_ACTIVE : EDGE_IDLE,
          strokeWidth: 1.5,
          strokeDasharray: getEdgeStatus(step, 'user-sidecar-b') === 'active' || highlightXt ? '0' : '5,5',
        },
        markerEnd: {
          type: MarkerType.ArrowClosed,
          color: getEdgeStatus(step, 'user-sidecar-b') === 'active' || highlightXt ? EDGE_ACTIVE : EDGE_IDLE,
        },
      })
    }

    if (showFlashblocks) {
      es.push({
        id: 'user-builder-a',
        source: 'user',
        target: 'builder-a',
        animated: false,
        label: 'normal tx',
        labelStyle: highlightNormal ? LSTYLE_ACTIVE(EDGE_ACTIVE) : LSTYLE(EDGE_ACTIVE),
        labelBgStyle: LBG,
        style: {
          stroke: highlightNormal ? EDGE_ACTIVE : EDGE_IDLE,
          strokeWidth: 1.5,
          strokeDasharray: highlightNormal ? '0' : '5,5',
        },
        markerEnd: { type: MarkerType.ArrowClosed, color: highlightNormal ? EDGE_ACTIVE : EDGE_IDLE },
      })
      es.push({
        id: 'user-builder-b',
        source: 'user',
        target: 'builder-b',
        animated: false,
        label: 'normal tx',
        labelStyle: highlightNormal ? LSTYLE_ACTIVE(EDGE_ACTIVE) : LSTYLE(EDGE_ACTIVE),
        labelBgStyle: LBG,
        style: {
          stroke: highlightNormal ? EDGE_ACTIVE : EDGE_IDLE,
          strokeWidth: 1.5,
          strokeDasharray: highlightNormal ? '0' : '5,5',
        },
        markerEnd: { type: MarkerType.ArrowClosed, color: highlightNormal ? EDGE_ACTIVE : EDGE_IDLE },
      })

      es.push({
        id: 'op-node-a-boost-a',
        source: 'op-node-a',
        target: 'boost-a',
        sourceHandle: 'to-boost',
        targetHandle: 'from-rollup',
        animated: getEdgeStatus(step, 'op-node-a-boost-a') === 'active',
        label: 'engine api',
        labelStyle:
          getEdgeStatus(step, 'op-node-a-boost-a') === 'active'
            ? LSTYLE_ACTIVE(EDGE_MINT_ACTIVE)
            : LSTYLE(EDGE_MINT_ACTIVE),
        labelBgStyle: LBG,
        style: {
          stroke: getEdgeStatus(step, 'op-node-a-boost-a') === 'active' ? EDGE_MINT_ACTIVE : EDGE_IDLE,
          strokeWidth: getEdgeStatus(step, 'op-node-a-boost-a') === 'active' ? 3 : 1.5,
        },
        markerEnd: {
          type: MarkerType.ArrowClosed,
          color: getEdgeStatus(step, 'op-node-a-boost-a') === 'active' ? EDGE_MINT_ACTIVE : EDGE_IDLE,
        },
      })
      es.push({
        id: 'op-node-b-boost-b',
        source: 'op-node-b',
        target: 'boost-b',
        sourceHandle: 'to-boost',
        targetHandle: 'from-rollup',
        animated: getEdgeStatus(step, 'op-node-b-boost-b') === 'active',
        label: 'engine api',
        labelStyle:
          getEdgeStatus(step, 'op-node-b-boost-b') === 'active'
            ? LSTYLE_ACTIVE(EDGE_MINT_ACTIVE)
            : LSTYLE(EDGE_MINT_ACTIVE),
        labelBgStyle: LBG,
        style: {
          stroke: getEdgeStatus(step, 'op-node-b-boost-b') === 'active' ? EDGE_MINT_ACTIVE : EDGE_IDLE,
          strokeWidth: getEdgeStatus(step, 'op-node-b-boost-b') === 'active' ? 3 : 1.5,
        },
        markerEnd: {
          type: MarkerType.ArrowClosed,
          color: getEdgeStatus(step, 'op-node-b-boost-b') === 'active' ? EDGE_MINT_ACTIVE : EDGE_IDLE,
        },
      })
      es.push({
        id: 'boost-a-builder-a',
        source: 'boost-a',
        target: 'builder-a',
        sourceHandle: 'to-builder',
        targetHandle: 'from-rollup',
        animated: getEdgeStatus(step, 'boost-a-builder-a') === 'active',
        label: 'engine api',
        labelStyle:
          getEdgeStatus(step, 'boost-a-builder-a') === 'active'
            ? LSTYLE_ACTIVE(EDGE_MINT_ACTIVE)
            : LSTYLE(EDGE_MINT_ACTIVE),
        labelBgStyle: LBG,
        style: {
          stroke: getEdgeStatus(step, 'boost-a-builder-a') === 'active' ? EDGE_MINT_ACTIVE : EDGE_IDLE,
          strokeWidth: getEdgeStatus(step, 'boost-a-builder-a') === 'active' ? 3 : 1.5,
        },
        markerEnd: {
          type: MarkerType.ArrowClosed,
          color: getEdgeStatus(step, 'boost-a-builder-a') === 'active' ? EDGE_MINT_ACTIVE : EDGE_IDLE,
        },
        markerStart: {
          type: MarkerType.ArrowClosed,
          color: getEdgeStatus(step, 'boost-a-builder-a') === 'active' ? EDGE_MINT_ACTIVE : EDGE_IDLE,
        },
      })
      es.push({
        id: 'boost-b-builder-b',
        source: 'boost-b',
        target: 'builder-b',
        sourceHandle: 'to-builder',
        targetHandle: 'from-rollup',
        animated: getEdgeStatus(step, 'boost-b-builder-b') === 'active',
        label: 'engine api',
        labelStyle:
          getEdgeStatus(step, 'boost-b-builder-b') === 'active'
            ? LSTYLE_ACTIVE(EDGE_MINT_ACTIVE)
            : LSTYLE(EDGE_MINT_ACTIVE),
        labelBgStyle: LBG,
        style: {
          stroke: getEdgeStatus(step, 'boost-b-builder-b') === 'active' ? EDGE_MINT_ACTIVE : EDGE_IDLE,
          strokeWidth: getEdgeStatus(step, 'boost-b-builder-b') === 'active' ? 3 : 1.5,
        },
        markerEnd: {
          type: MarkerType.ArrowClosed,
          color: getEdgeStatus(step, 'boost-b-builder-b') === 'active' ? EDGE_MINT_ACTIVE : EDGE_IDLE,
        },
        markerStart: {
          type: MarkerType.ArrowClosed,
          color: getEdgeStatus(step, 'boost-b-builder-b') === 'active' ? EDGE_MINT_ACTIVE : EDGE_IDLE,
        },
      })

      es.push({
        id: 'boost-a-op-reth-a',
        source: 'boost-a',
        target: 'op-reth-a',
        animated: false,
        label: 'fallback',
        labelStyle: { fontSize: 9, fontFamily: '"IBM Plex Mono", monospace', fill: '#606075' },
        labelBgStyle: LBG,
        style: { stroke: '#404055', strokeWidth: 1.5, strokeDasharray: '4,4' },
        markerEnd: { type: MarkerType.ArrowClosed, color: '#404055' },
      })
      es.push({
        id: 'boost-b-op-reth-b',
        source: 'boost-b',
        target: 'op-reth-b',
        animated: false,
        label: 'fallback',
        labelStyle: { fontSize: 9, fontFamily: '"IBM Plex Mono", monospace', fill: '#606075' },
        labelBgStyle: LBG,
        style: { stroke: '#404055', strokeWidth: 1.5, strokeDasharray: '4,4' },
        markerEnd: { type: MarkerType.ArrowClosed, color: '#404055' },
      })
    }

    if (showSidecars && showFlashblocks) {
      es.push({
        id: 'sidecar-a-simulate-builder-a',
        source: 'sidecar-a',
        target: 'builder-a',
        type: 'smoothstep',
        pathOptions: { offset: 20 },
        animated: getEdgeStatus(step, 'sidecar-a-simulate-builder-a') === 'active',
        label: 'simulate/trace',
        labelStyle:
          getEdgeStatus(step, 'sidecar-a-simulate-builder-a') === 'active' || highlightXt
            ? LSTYLE_ACTIVE('#60A5FA')
            : LSTYLE('#60A5FA'),
        labelBgStyle: LBG,
        style: {
          stroke:
            getEdgeStatus(step, 'sidecar-a-simulate-builder-a') === 'active' || highlightXt
              ? '#60A5FA'
              : EDGE_IDLE,
          strokeWidth: getEdgeStatus(step, 'sidecar-a-simulate-builder-a') === 'active' ? 2.5 : 1.5,
          strokeDasharray:
            getEdgeStatus(step, 'sidecar-a-simulate-builder-a') === 'active' || highlightXt ? '0' : '4,4',
        },
        markerEnd: {
          type: MarkerType.ArrowClosed,
          color:
            getEdgeStatus(step, 'sidecar-a-simulate-builder-a') === 'active' || highlightXt
              ? '#60A5FA'
              : EDGE_IDLE,
        },
      })
      es.push({
        id: 'sidecar-b-simulate-builder-b',
        source: 'sidecar-b',
        target: 'builder-b',
        type: 'smoothstep',
        pathOptions: { offset: 20 },
        animated: getEdgeStatus(step, 'sidecar-b-simulate-builder-b') === 'active',
        label: 'simulate/trace',
        labelStyle:
          getEdgeStatus(step, 'sidecar-b-simulate-builder-b') === 'active' || highlightXt
            ? LSTYLE_ACTIVE('#60A5FA')
            : LSTYLE('#60A5FA'),
        labelBgStyle: LBG,
        style: {
          stroke:
            getEdgeStatus(step, 'sidecar-b-simulate-builder-b') === 'active' || highlightXt
              ? '#60A5FA'
              : EDGE_IDLE,
          strokeWidth: getEdgeStatus(step, 'sidecar-b-simulate-builder-b') === 'active' ? 2.5 : 1.5,
          strokeDasharray:
            getEdgeStatus(step, 'sidecar-b-simulate-builder-b') === 'active' || highlightXt ? '0' : '4,4',
        },
        markerEnd: {
          type: MarkerType.ArrowClosed,
          color:
            getEdgeStatus(step, 'sidecar-b-simulate-builder-b') === 'active' || highlightXt
              ? '#60A5FA'
              : EDGE_IDLE,
        },
      })

      es.push({
        id: 'sidecar-a-builder-a',
        source: 'sidecar-a',
        target: 'builder-a',
        type: 'smoothstep',
        pathOptions: { offset: -20 },
        animated: getEdgeStatus(step, 'sidecar-a-builder-a') === 'active',
        label: 'ethera_submitXt / releaseXt',
        labelStyle:
          getEdgeStatus(step, 'sidecar-a-builder-a') === 'active'
            ? LSTYLE_ACTIVE('#C084FC')
            : LSTYLE('#C084FC'),
        labelBgStyle: LBG,
        style: {
          stroke: getEdgeStatus(step, 'sidecar-a-builder-a') === 'active' ? '#C084FC' : EDGE_IDLE,
          strokeWidth: getEdgeStatus(step, 'sidecar-a-builder-a') === 'active' ? 2.5 : 1.5,
        },
        markerEnd: {
          type: MarkerType.ArrowClosed,
          color: getEdgeStatus(step, 'sidecar-a-builder-a') === 'active' ? '#C084FC' : EDGE_IDLE,
        },
      })
      es.push({
        id: 'sidecar-b-builder-b',
        source: 'sidecar-b',
        target: 'builder-b',
        type: 'smoothstep',
        pathOptions: { offset: -20 },
        animated: getEdgeStatus(step, 'sidecar-b-builder-b') === 'active',
        label: 'ethera_submitXt / releaseXt',
        labelStyle:
          getEdgeStatus(step, 'sidecar-b-builder-b') === 'active'
            ? LSTYLE_ACTIVE('#C084FC')
            : LSTYLE('#C084FC'),
        labelBgStyle: LBG,
        style: {
          stroke: getEdgeStatus(step, 'sidecar-b-builder-b') === 'active' ? '#C084FC' : EDGE_IDLE,
          strokeWidth: getEdgeStatus(step, 'sidecar-b-builder-b') === 'active' ? 2.5 : 1.5,
        },
        markerEnd: {
          type: MarkerType.ArrowClosed,
          color: getEdgeStatus(step, 'sidecar-b-builder-b') === 'active' ? '#C084FC' : EDGE_IDLE,
        },
      })
      es.push({
        id: 'builder-a-sidecar-a',
        source: 'builder-a',
        target: 'sidecar-a',
        animated: getEdgeStatus(step, 'builder-a-sidecar-a') === 'active',
        label: 'POST /ethera/confirm',
        labelStyle:
          getEdgeStatus(step, 'builder-a-sidecar-a') === 'active'
            ? LSTYLE_ACTIVE('#FBBF24')
            : LSTYLE('#FBBF24'),
        labelBgStyle: LBG,
        style: {
          stroke: getEdgeStatus(step, 'builder-a-sidecar-a') === 'active' ? '#FBBF24' : EDGE_IDLE,
          strokeWidth: getEdgeStatus(step, 'builder-a-sidecar-a') === 'active' ? 2.5 : 1.5,
        },
        markerEnd: {
          type: MarkerType.ArrowClosed,
          color: getEdgeStatus(step, 'builder-a-sidecar-a') === 'active' ? '#FBBF24' : EDGE_IDLE,
        },
      })
      es.push({
        id: 'builder-b-sidecar-b',
        source: 'builder-b',
        target: 'sidecar-b',
        animated: getEdgeStatus(step, 'builder-b-sidecar-b') === 'active',
        label: 'POST /ethera/confirm',
        labelStyle:
          getEdgeStatus(step, 'builder-b-sidecar-b') === 'active'
            ? LSTYLE_ACTIVE('#FBBF24')
            : LSTYLE('#FBBF24'),
        labelBgStyle: LBG,
        style: {
          stroke: getEdgeStatus(step, 'builder-b-sidecar-b') === 'active' ? '#FBBF24' : EDGE_IDLE,
          strokeWidth: getEdgeStatus(step, 'builder-b-sidecar-b') === 'active' ? 2.5 : 1.5,
        },
        markerEnd: {
          type: MarkerType.ArrowClosed,
          color: getEdgeStatus(step, 'builder-b-sidecar-b') === 'active' ? '#FBBF24' : EDGE_IDLE,
        },
      })
    }

    if (showSidecars) {
      es.push({
        id: 'sidecar-a-sidecar-b',
        source: 'sidecar-a',
        target: 'sidecar-b',
        animated: getEdgeStatus(step, 'sidecar-a-sidecar-b') === 'active',
        label: 'cross-chain coordination',
        labelStyle:
          getEdgeStatus(step, 'sidecar-a-sidecar-b') === 'active'
            ? { fontSize: 10, fontFamily: '"IBM Plex Mono", monospace', fill: '#FF6B00', fontWeight: 600 }
            : { fontSize: 10, fontFamily: '"IBM Plex Mono", monospace', fill: '#9090B0' },
        labelBgStyle: LBG,
        style: {
          stroke: getEdgeStatus(step, 'sidecar-a-sidecar-b') === 'active' ? '#FF6B00' : '#7070A0',
          strokeWidth: getEdgeStatus(step, 'sidecar-a-sidecar-b') === 'active' ? 3.5 : 2,
        },
        markerEnd: {
          type: MarkerType.ArrowClosed,
          color: getEdgeStatus(step, 'sidecar-a-sidecar-b') === 'active' ? '#FF6B00' : '#7070A0',
        },
        markerStart: {
          type: MarkerType.ArrowClosed,
          color: getEdgeStatus(step, 'sidecar-a-sidecar-b') === 'active' ? '#FF6B00' : '#7070A0',
        },
      })
      es.push({
        id: 'sidecar-a-publisher',
        source: 'sidecar-a',
        target: 'publisher',
        animated: getEdgeStatus(step, 'sidecar-a-publisher') === 'active',
        label: 'vote',
        labelStyle:
          getEdgeStatus(step, 'sidecar-a-publisher') === 'active'
            ? LSTYLE_ACTIVE('#A78BFA')
            : LSTYLE('#A78BFA'),
        labelBgStyle: LBG,
        style: {
          stroke: getEdgeStatus(step, 'sidecar-a-publisher') === 'active' ? '#A78BFA' : EDGE_IDLE,
          strokeWidth: getEdgeStatus(step, 'sidecar-a-publisher') === 'active' ? 2.5 : 1.5,
        },
        markerEnd: {
          type: MarkerType.ArrowClosed,
          color: getEdgeStatus(step, 'sidecar-a-publisher') === 'active' ? '#A78BFA' : EDGE_IDLE,
        },
      })
      es.push({
        id: 'sidecar-b-publisher',
        source: 'sidecar-b',
        target: 'publisher',
        animated: getEdgeStatus(step, 'sidecar-b-publisher') === 'active',
        label: 'vote',
        labelStyle:
          getEdgeStatus(step, 'sidecar-b-publisher') === 'active'
            ? LSTYLE_ACTIVE('#A78BFA')
            : LSTYLE('#A78BFA'),
        labelBgStyle: LBG,
        style: {
          stroke: getEdgeStatus(step, 'sidecar-b-publisher') === 'active' ? '#A78BFA' : EDGE_IDLE,
          strokeWidth: getEdgeStatus(step, 'sidecar-b-publisher') === 'active' ? 2.5 : 1.5,
        },
        markerEnd: {
          type: MarkerType.ArrowClosed,
          color: getEdgeStatus(step, 'sidecar-b-publisher') === 'active' ? '#A78BFA' : EDGE_IDLE,
        },
      })
      es.push({
        id: 'publisher-sidecar-a',
        source: 'publisher',
        target: 'sidecar-a',
        animated: getEdgeStatus(step, 'publisher-sidecar-a') === 'active',
        label: 'start/decide',
        labelStyle:
          getEdgeStatus(step, 'publisher-sidecar-a') === 'active' || highlightXt
            ? LSTYLE_ACTIVE('#A78BFA')
            : LSTYLE('#A78BFA'),
        labelBgStyle: LBG,
        style: {
          stroke:
            getEdgeStatus(step, 'publisher-sidecar-a') === 'active' || highlightXt ? '#A78BFA' : EDGE_IDLE,
          strokeWidth: getEdgeStatus(step, 'publisher-sidecar-a') === 'active' ? 2.5 : 1.5,
          strokeDasharray:
            getEdgeStatus(step, 'publisher-sidecar-a') === 'active' || highlightXt ? '0' : '4,4',
        },
        markerEnd: {
          type: MarkerType.ArrowClosed,
          color:
            getEdgeStatus(step, 'publisher-sidecar-a') === 'active' || highlightXt ? '#A78BFA' : EDGE_IDLE,
        },
      })
      es.push({
        id: 'publisher-sidecar-b',
        source: 'publisher',
        target: 'sidecar-b',
        animated: getEdgeStatus(step, 'publisher-sidecar-b') === 'active',
        label: 'start/decide',
        labelStyle:
          getEdgeStatus(step, 'publisher-sidecar-b') === 'active' || highlightXt
            ? LSTYLE_ACTIVE('#A78BFA')
            : LSTYLE('#A78BFA'),
        labelBgStyle: LBG,
        style: {
          stroke:
            getEdgeStatus(step, 'publisher-sidecar-b') === 'active' || highlightXt ? '#A78BFA' : EDGE_IDLE,
          strokeWidth: getEdgeStatus(step, 'publisher-sidecar-b') === 'active' ? 2.5 : 1.5,
          strokeDasharray:
            getEdgeStatus(step, 'publisher-sidecar-b') === 'active' || highlightXt ? '0' : '4,4',
        },
        markerEnd: {
          type: MarkerType.ArrowClosed,
          color:
            getEdgeStatus(step, 'publisher-sidecar-b') === 'active' || highlightXt ? '#A78BFA' : EDGE_IDLE,
        },
      })
    }

    es.push({
      id: 'op-reth-a-batcher-a',
      source: 'op-reth-a',
      target: 'op-batcher-a',
      animated: false,
      label: 'batch',
      labelStyle: LSTYLE('#9090B0'),
      labelBgStyle: LBG,
      style: { stroke: '#404055', strokeWidth: 1.5, strokeDasharray: '4,4' },
      markerEnd: { type: MarkerType.ArrowClosed, color: '#404055' },
    })
    es.push({
      id: 'op-reth-b-batcher-b',
      source: 'op-reth-b',
      target: 'op-batcher-b',
      animated: false,
      label: 'batch',
      labelStyle: LSTYLE('#9090B0'),
      labelBgStyle: LBG,
      style: { stroke: '#404055', strokeWidth: 1.5, strokeDasharray: '4,4' },
      markerEnd: { type: MarkerType.ArrowClosed, color: '#404055' },
    })
    es.push({
      id: 'op-reth-a-proposer-a',
      source: 'op-reth-a',
      target: 'op-proposer-a',
      animated: false,
      label: 'propose',
      labelStyle: LSTYLE('#9090B0'),
      labelBgStyle: LBG,
      style: { stroke: '#404055', strokeWidth: 1.5, strokeDasharray: '4,4' },
      markerEnd: { type: MarkerType.ArrowClosed, color: '#404055' },
    })
    es.push({
      id: 'op-reth-b-proposer-b',
      source: 'op-reth-b',
      target: 'op-proposer-b',
      animated: false,
      label: 'propose',
      labelStyle: LSTYLE('#9090B0'),
      labelBgStyle: LBG,
      style: { stroke: '#404055', strokeWidth: 1.5, strokeDasharray: '4,4' },
      markerEnd: { type: MarkerType.ArrowClosed, color: '#404055' },
    })

    if (showAltDa) {
      es.push({
        id: 'batcher-a-altda-a',
        source: 'op-batcher-a',
        target: 'op-alt-da-a',
        animated: false,
        label: 'da put',
        labelStyle: LSTYLE('#7DD3FC'),
        labelBgStyle: LBG,
        style: { stroke: '#7DD3FC', strokeWidth: 1.5, strokeDasharray: '4,4' },
        markerEnd: { type: MarkerType.ArrowClosed, color: '#7DD3FC' },
      })
      es.push({
        id: 'batcher-b-altda-b',
        source: 'op-batcher-b',
        target: 'op-alt-da-b',
        animated: false,
        label: 'da put',
        labelStyle: LSTYLE('#7DD3FC'),
        labelBgStyle: LBG,
        style: { stroke: '#7DD3FC', strokeWidth: 1.5, strokeDasharray: '4,4' },
        markerEnd: { type: MarkerType.ArrowClosed, color: '#7DD3FC' },
      })
    }

    if (showOpSuccinct) {
      es.push({
        id: 'op-reth-a-opsuccinct-a',
        source: 'op-reth-a',
        target: 'op-succinct-a',
        animated: false,
        label: 'range proof',
        labelStyle: LSTYLE('#F0ABFC'),
        labelBgStyle: LBG,
        style: { stroke: '#A78BFA', strokeWidth: 1.5, strokeDasharray: '4,4' },
        markerEnd: { type: MarkerType.ArrowClosed, color: '#A78BFA' },
      })
      es.push({
        id: 'op-reth-b-opsuccinct-b',
        source: 'op-reth-b',
        target: 'op-succinct-b',
        animated: false,
        label: 'range proof',
        labelStyle: LSTYLE('#F0ABFC'),
        labelBgStyle: LBG,
        style: { stroke: '#A78BFA', strokeWidth: 1.5, strokeDasharray: '4,4' },
        markerEnd: { type: MarkerType.ArrowClosed, color: '#A78BFA' },
      })
      es.push({
        id: 'opsuccinct-a-postgres',
        source: 'op-succinct-a',
        target: 'op-succinct-postgres',
        animated: false,
        label: 'state',
        labelStyle: LSTYLE('#A78BFA'),
        labelBgStyle: LBG,
        style: { stroke: '#A78BFA', strokeWidth: 1.5, strokeDasharray: '4,4' },
        markerEnd: { type: MarkerType.ArrowClosed, color: '#A78BFA' },
      })
      es.push({
        id: 'opsuccinct-b-postgres',
        source: 'op-succinct-b',
        target: 'op-succinct-postgres',
        animated: false,
        label: 'state',
        labelStyle: LSTYLE('#A78BFA'),
        labelBgStyle: LBG,
        style: { stroke: '#A78BFA', strokeWidth: 1.5, strokeDasharray: '4,4' },
        markerEnd: { type: MarkerType.ArrowClosed, color: '#A78BFA' },
      })
    }

    return es
  }, [highlightNormal, highlightXt, step, showFlashblocks, showSidecars, showAltDa, showOpSuccinct])

  const onEdgeClick = useCallback(
    (_event: MouseEvent, edge: Edge) => {
      if (!onSelectFlow) {
        return
      }
      if (edge.id === 'user-builder-a' || edge.id === 'user-builder-b') {
        onSelectFlow('normal')
      }
      if (edge.id === 'user-sidecar-a' || edge.id === 'user-sidecar-b') {
        onSelectFlow('xt')
      }
    },
    [onSelectFlow],
  )

  if (isFullscreen) {
    return (
      <>
        <div
          className="fixed inset-0 z-[9999] bg-black/60 backdrop-blur-sm flex items-center justify-center p-8"
          onClick={() => setIsFullscreen(false)}
        >
          <div
            className="w-full h-full max-w-[95vw] max-h-[95vh] shadow-2xl border border-border relative overflow-hidden"
            style={{ background: '#0A0A0C' }}
            onClick={(e) => e.stopPropagation()}
          >
            <ReactFlow
              nodes={nodes}
              edges={edges}
              nodeTypes={nodeTypes}
              onEdgeClick={onEdgeClick}
              fitView
              attributionPosition="bottom-left"
              proOptions={{ hideAttribution: true }}
              style={{ background: '#0A0A0C' }}
            >
              <Background color="#1A1A24" gap={32} />
              <Controls showInteractive={false} />
            </ReactFlow>

            <div className="absolute bottom-6 left-6 bg-bg-card/95 border border-border px-4 py-3 z-10">
              <p className="text-[9px] font-display tracking-widest uppercase text-text-dim">Step</p>
              <p className="text-xs font-mono text-text-secondary capitalize">
                {step.replace(/_/g, ' ')}
              </p>
            </div>

            <button
              onClick={() => setIsFullscreen(false)}
              className="absolute top-6 right-6 z-10 bg-bg-card/95 border border-border px-4 py-2.5 hover:border-amber hover:text-amber transition-colors text-[10px] font-display tracking-widest uppercase text-text-secondary flex items-center gap-2"
            >
              <svg xmlns="http://www.w3.org/2000/svg" width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2.5" strokeLinecap="square">
                <path d="M18 6 6 18M6 6l12 12" />
              </svg>
              Close
            </button>

            <div className="absolute top-6 left-6 z-10 bg-bg-card/95 border border-border px-3 py-2 text-[10px] font-mono text-text-dim">
              <kbd className="font-display tracking-widest">ESC</kbd> to exit
            </div>
          </div>
        </div>
      </>
    )
  }

  return (
    <div className="w-full h-full">
      <ReactFlow
        nodes={nodes}
        edges={edges}
        nodeTypes={nodeTypes}
        onEdgeClick={onEdgeClick}
        onInit={(instance) => { rfInstance.current = instance }}
        attributionPosition="bottom-left"
        proOptions={{ hideAttribution: true }}
        style={{ background: '#0D0D14' }}
      >
        <Background color="#252535" gap={32} />
        <Controls showInteractive={false} />
        <AutoFit nodeCount={nodes.length} />
      </ReactFlow>

      <button
        onClick={() => setIsFullscreen(true)}
        className="absolute top-4 right-4 bg-bg-card/95 border border-border px-3 py-2 hover:border-amber hover:text-amber transition-colors text-[10px] font-display tracking-widest uppercase text-text-secondary flex items-center gap-2"
      >
        <svg
          xmlns="http://www.w3.org/2000/svg"
          width="16"
          height="16"
          viewBox="0 0 24 24"
          fill="none"
          stroke="currentColor"
          strokeWidth="2"
          strokeLinecap="round"
          strokeLinejoin="round"
        >
          <path d="M8 3H5a2 2 0 0 0-2 2v3m18 0V5a2 2 0 0 0-2-2h-3m0 18h3a2 2 0 0 0 2-2v-3M3 16v3a2 2 0 0 0 2 2h3" />
        </svg>
        Fullscreen
      </button>
    </div>
  )
}
