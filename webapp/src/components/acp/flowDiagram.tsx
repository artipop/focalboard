// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.
import React, {useEffect, useMemo} from 'react'
import {useIntl, IntlShape} from 'react-intl'
import {
    Background,
    Controls,
    Edge,
    Handle,
    MarkerType,
    Node,
    NodeProps,
    Position,
    ReactFlow,
    useEdgesState,
    useNodesState,
} from '@xyflow/react'

import {FlowEdge, FlowNode, FlowTrigger, SUCCESS, FAILURE} from './workflowsDialog'

import '@xyflow/react/dist/style.css'
import './flowDiagram.scss'

// The route drawn as a graph, which is what it is: stages left to right in the
// order the card travels, transitions as arrows — green for success, red for
// failure, grey and labelled for anything the stage waits on. Pan, zoom and
// drag are React Flow's; the layout below is ours, because a route read top to
// bottom in the editor should read left to right here.

export const NODE_WIDTH = 190
export const NODE_HEIGHT = 58
const GAP_X = 80
const GAP_Y = 24

type Props = {
    nodes: FlowNode[]
    edges: FlowEdge[]
    triggers: FlowTrigger[]
}

type StageData = {
    column: string
    action: string
    actionLabel: string
}

// forward drops the transitions that lead back the way the card came — a failed
// check returning to the agent is a normal route, and laying it out as progress
// would push every later stage off to the right for ever.
function forward(nodes: FlowNode[], edges: FlowEdge[]): FlowEdge[] {
    const out = new Map<string, FlowEdge[]>(nodes.map((n) => [n.id, []]))
    for (const edge of edges) {
        if (out.has(edge.from) && out.has(edge.to) && edge.from !== edge.to) {
            (out.get(edge.from) as FlowEdge[]).push(edge)
        }
    }

    const kept: FlowEdge[] = []
    const state = new Map<string, 'open' | 'done'>()

    // Depth-first, iteratively: an edge into a stage still on the stack closes
    // a loop and is left out of the layout.
    const walk = (start: string) => {
        const stack: Array<{id: string, next: number}> = [{id: start, next: 0}]
        state.set(start, 'open')
        while (stack.length > 0) {
            const top = stack[stack.length - 1]
            const outgoing = out.get(top.id) as FlowEdge[]
            if (top.next >= outgoing.length) {
                state.set(top.id, 'done')
                stack.pop()
                continue
            }
            const edge = outgoing[top.next++]
            if (state.get(edge.to) === 'open') {
                continue
            }
            kept.push(edge)
            if (!state.has(edge.to)) {
                state.set(edge.to, 'open')
                stack.push({id: edge.to, next: 0})
            }
        }
    }

    // Start where a card would: at the stages nothing leads to, then at
    // whatever the walk has not reached (a route that is one closed loop).
    const reached = new Set(edges.map((e) => e.to))
    for (const node of nodes.filter((n) => !reached.has(n.id))) {
        walk(node.id)
    }
    for (const node of nodes) {
        if (!state.has(node.id)) {
            walk(node.id)
        }
    }
    return kept
}

// depths puts every stage as far right as the longest path that reaches it, so
// an arrow points forward wherever the route does.
export function depths(nodes: FlowNode[], edges: FlowEdge[]): Map<string, number> {
    const depth = new Map<string, number>(nodes.map((n) => [n.id, 0]))
    const dag = forward(nodes, edges)
    for (let pass = 0; pass < nodes.length; pass++) {
        let moved = false
        for (const edge of dag) {
            const next = (depth.get(edge.from) as number) + 1
            if ((depth.get(edge.to) as number) < next) {
                depth.set(edge.to, next)
                moved = true
            }
        }
        if (!moved) {
            break
        }
    }
    return depth
}

// layout places the stages in columns of equal depth, keeping the editor's own
// order within a column.
export function layout(nodes: FlowNode[], edges: FlowEdge[]): Map<string, {x: number, y: number}> {
    const depth = depths(nodes, edges)
    const taken = new Map<number, number>()
    const out = new Map<string, {x: number, y: number}>()
    for (const node of nodes) {
        const column = depth.get(node.id) || 0
        const row = taken.get(column) || 0
        taken.set(column, row + 1)
        out.set(node.id, {
            x: column * (NODE_WIDTH + GAP_X),
            y: row * (NODE_HEIGHT + GAP_Y),
        })
    }
    return out
}

// An arrow and its head must be one colour, and the head is drawn from a shared
// SVG marker that no class of ours can reach — so the colours are literals here
// rather than CSS variables. They are picked to read on a light and a dark
// board alike.
const EDGE_COLOR: Record<string, string> = {
    success: '#3db887',
    failure: '#d24b4e',
    event: '#8b8d94',
}

// edgeKind styles a transition by what produces it: the stage's own outcome, or
// something that happened in the repository.
export function edgeKind(on: string): string {
    if (on === SUCCESS) {
        return 'success'
    }
    if (on === FAILURE) {
        return 'failure'
    }
    return 'event'
}

// StageNode is one stage: the column a card sits in, and what runs when it
// lands there.
const StageNode = (props: NodeProps) => {
    const data = props.data as StageData
    return (
        <div className={`FlowDiagram__stage FlowDiagram__stage--${data.action || 'none'}`}>
            <Handle
                type='target'
                position={Position.Left}
            />
            <div className='FlowDiagram__column'>{data.column || '—'}</div>
            <div className='FlowDiagram__action'>{data.actionLabel}</div>
            <Handle
                type='source'
                position={Position.Right}
            />
        </div>
    )
}

const nodeTypes = {stage: StageNode}

// stageLabel names what a stage does, in the reader's language. Kept short:
// this is a box on a canvas, not a form field.
function stageLabel(intl: IntlShape, action: string): string {
    switch (action) {
    case 'agent':
        return intl.formatMessage({id: 'FlowDiagram.action-agent', defaultMessage: 'agent'})
    case 'deploy':
        return intl.formatMessage({id: 'FlowDiagram.action-deploy', defaultMessage: 'deploy'})
    case 'test':
        return intl.formatMessage({id: 'FlowDiagram.action-test', defaultMessage: 'test'})
    default:
        return intl.formatMessage({id: 'FlowDiagram.action-none', defaultMessage: 'waits'})
    }
}

const FlowDiagram = (props: Props) => {
    const {nodes, edges, triggers} = props
    const intl = useIntl()

    const graph = useMemo(() => {
        const positions = layout(nodes, edges)
        const rfNodes: Node[] = nodes.map((node) => ({
            id: node.id,
            type: 'stage',
            position: positions.get(node.id) || {x: 0, y: 0},

            // Stated rather than measured: the box is a fixed size in CSS and
            // its handles sit at fixed points, so the arrows are drawn on the
            // first paint instead of after the browser reports a layout.
            width: NODE_WIDTH,
            height: NODE_HEIGHT,
            handles: [
                {type: 'target', position: Position.Left, x: 0, y: NODE_HEIGHT / 2},
                {type: 'source', position: Position.Right, x: NODE_WIDTH, y: NODE_HEIGHT / 2},
            ],
            data: {
                column: node.column,
                action: node.action,
                actionLabel: stageLabel(intl, node.action),
            },
            sourcePosition: Position.Right,
            targetPosition: Position.Left,
        }))

        const known = new Set(nodes.map((n) => n.id))
        const rfEdges: Edge[] = edges.filter((e) => known.has(e.from) && known.has(e.to)).map((edge) => {
            const kind = edgeKind(edge.on)
            const color = EDGE_COLOR[kind]
            const label = kind === 'event' ? (triggers.find((t) => t.kind === edge.on)?.label || edge.on) : ''
            return {
                id: `${edge.from}-${edge.on}`,
                source: edge.from,
                target: edge.to,
                type: 'smoothstep',
                className: `FlowDiagram__edge FlowDiagram__edge--${kind}`,
                label,
                style: {stroke: color, strokeWidth: 1.5, strokeDasharray: kind === 'event' ? '4 3' : undefined},
                markerEnd: {type: MarkerType.ArrowClosed, width: 16, height: 16, color},
            }
        })
        return {rfNodes, rfEdges}
    }, [nodes, edges, triggers, intl])

    // Stages can be dragged apart when arrows overlap; the position is the
    // reader's, not the route's, so nothing is saved and editing the route
    // lays it out again.
    const [drawnNodes, setDrawnNodes, onNodesChange] = useNodesState(graph.rfNodes)
    const [drawnEdges, setDrawnEdges] = useEdgesState(graph.rfEdges)

    useEffect(() => {
        setDrawnNodes(graph.rfNodes)
        setDrawnEdges(graph.rfEdges)
    }, [graph, setDrawnNodes, setDrawnEdges])

    if (nodes.length === 0) {
        return null
    }

    return (
        <div
            className='FlowDiagram'
            data-testid='flow-diagram'
        >
            <ReactFlow
                nodes={drawnNodes}
                edges={drawnEdges}
                onNodesChange={onNodesChange}
                nodeTypes={nodeTypes}
                nodesConnectable={false}
                edgesFocusable={false}
                fitView={true}
                fitViewOptions={{padding: 0.2, maxZoom: 1}}
                minZoom={0.3}
                proOptions={{hideAttribution: false}}
            >
                <Background/>
                <Controls showInteractive={false}/>
            </ReactFlow>
        </div>
    )
}

export default React.memo(FlowDiagram)
