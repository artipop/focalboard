// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.
import React from 'react'

import {FlowEdge, FlowNode, FlowTrigger, SUCCESS, FAILURE} from './workflowsDialog'

// The route drawn as a graph. Stages sit in a column in the order they are
// listed; transitions arc to the right of them — solid for success, dashed for
// failure, dotted with a label for anything the stage waits on. Hand-drawn SVG
// rather than a graph library: React 17 and no new frontend dependencies.

const BOX_W = 200
const BOX_H = 40
const GAP = 28
const LEFT = 12
const LANE = 26 // horizontal distance between edge lanes

type Props = {
    nodes: FlowNode[]
    edges: FlowEdge[]
    triggers: FlowTrigger[]
}

// edgeClass styles a transition by what produces it: the stage's own outcome,
// or something that happened in the repository.
function edgeClass(on: string): string {
    if (on === SUCCESS) {
        return 'success'
    }
    if (on === FAILURE) {
        return 'failure'
    }
    return 'event'
}

const FlowDiagram = (props: Props) => {
    const {nodes, edges, triggers} = props
    if (nodes.length === 0) {
        return null
    }

    const index = new Map(nodes.map((n, i) => [n.id, i]))
    const y = (i: number) => (i * (BOX_H + GAP)) + 10
    const height = y(nodes.length - 1) + BOX_H + 20

    // Edges are laid into lanes so two arcs never sit on top of each other.
    const drawn = edges.filter((e) => index.has(e.from) && index.has(e.to))
    const width = BOX_W + LEFT + (LANE * Math.max(drawn.length, 1)) + 160

    const label = (kind: string) => triggers.find((t) => t.kind === kind)?.label || kind

    return (
        <svg
            className='FlowDiagram'
            viewBox={`0 0 ${width} ${height}`}
            width='100%'
            height={height}
            role='img'
        >
            <defs>
                <marker
                    id='flowArrow'
                    viewBox='0 0 10 10'
                    refX='9'
                    refY='5'
                    markerWidth='6'
                    markerHeight='6'
                    orient='auto-start-reverse'
                >
                    <path
                        d='M 0 0 L 10 5 L 0 10 z'
                        fill='currentColor'
                    />
                </marker>
            </defs>

            {nodes.map((node, i) => (
                <g key={node.id}>
                    <rect
                        x={LEFT}
                        y={y(i)}
                        width={BOX_W}
                        height={BOX_H}
                        rx={8}
                        className={`FlowDiagram__box FlowDiagram__box--${node.action || 'none'}`}
                    />
                    <text
                        x={LEFT + 12}
                        y={y(i) + 17}
                        className='FlowDiagram__column'
                    >{node.column || '—'}</text>
                    <text
                        x={LEFT + 12}
                        y={y(i) + 31}
                        className='FlowDiagram__action'
                    >{node.action || 'none'}</text>
                </g>
            ))}

            {drawn.map((edge, lane) => {
                const from = index.get(edge.from) as number
                const to = index.get(edge.to) as number
                const x = LEFT + BOX_W + 8 + (lane * LANE)
                const y1 = y(from) + (BOX_H / 2)
                const y2 = y(to) + (BOX_H / 2)
                const kindClass = edgeClass(edge.on)
                return (
                    <g
                        key={`${edge.from}-${edge.on}`}
                        className={`FlowDiagram__edge FlowDiagram__edge--${kindClass}`}
                    >
                        <path
                            d={`M ${LEFT + BOX_W} ${y1} H ${x} V ${y2} H ${LEFT + BOX_W + 4}`}
                            fill='none'
                            markerEnd='url(#flowArrow)'
                        />
                        {edge.on !== SUCCESS && edge.on !== FAILURE &&
                            <text
                                x={x + 6}
                                y={(y1 + y2) / 2}
                                className='FlowDiagram__edgeLabel'
                            >{label(edge.on)}</text>}
                    </g>
                )
            })}
        </svg>
    )
}

export default React.memo(FlowDiagram)
