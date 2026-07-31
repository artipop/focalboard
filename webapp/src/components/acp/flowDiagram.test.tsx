// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.
import React from 'react'
import {render, screen} from '@testing-library/react'
import '@testing-library/jest-dom'

import {wrapIntl} from '../../testUtils'
import {setupReactFlowEnvironment} from '../../test/reactFlowEnvironment'

import FlowDiagram, {depths, layout, edgeKind, connectEdge, HANDLE_EVENT, HANDLE_FAILURE, HANDLE_SUCCESS, NODE_WIDTH} from './flowDiagram'
import {SUCCESS, FAILURE} from './workflowsDialog'

setupReactFlowEnvironment()

const nodes = [
    {id: 'agent', column: 'To Agent', action: 'agent'},
    {id: 'review', column: 'In Review', action: 'none'},
    {id: 'deploy', column: 'Deploy', action: 'deploy'},
    {id: 'test', column: 'To Test', action: 'test'},
]

const edges = [
    {from: 'agent', to: 'review', on: SUCCESS},
    {from: 'review', to: 'deploy', on: 'branch.merged'},
    {from: 'deploy', to: 'test', on: SUCCESS},

    // A failed check goes back to the agent: the route contains a cycle.
    {from: 'test', to: 'agent', on: FAILURE},
]

const triggers = [
    {kind: 'success', source: 'outcome', label: 'шаг прошёл'},
    {kind: 'failure', source: 'outcome', label: 'шаг упал'},
    {kind: 'branch.merged', source: 'git', label: 'ветка влита в основную'},
]

describe('components/acp/flowDiagram layout', () => {
    test('a stage sits to the right of everything that leads to it', () => {
        const depth = depths(nodes, edges)
        expect(depth.get('agent')).toBe(0)
        expect(depth.get('review')).toBe(1)
        expect(depth.get('deploy')).toBe(2)
        expect(depth.get('test')).toBe(3)

        const positions = layout(nodes, edges)
        expect(positions.get('review')!.x).toBe(positions.get('agent')!.x + NODE_WIDTH + 80)
    })

    test('stages of equal depth are stacked, not overlaid', () => {
        const branching = [
            {id: 'a', column: 'A', action: 'agent'},
            {id: 'ok', column: 'B', action: 'none'},
            {id: 'bad', column: 'C', action: 'none'},
        ]
        const positions = layout(branching, [
            {from: 'a', to: 'ok', on: SUCCESS},
            {from: 'a', to: 'bad', on: FAILURE},
        ])
        expect(positions.get('ok')!.x).toBe(positions.get('bad')!.x)
        expect(positions.get('ok')!.y).not.toBe(positions.get('bad')!.y)
    })

    test('a cycle lays out instead of spinning', () => {
        const loop = [
            {id: 'a', column: 'A', action: 'none'},
            {id: 'b', column: 'B', action: 'none'},
        ]
        const positions = layout(loop, [
            {from: 'a', to: 'b', on: SUCCESS},
            {from: 'b', to: 'a', on: FAILURE},
        ])
        expect(positions.size).toBe(2)
    })

    test('an edge is styled by what produces it', () => {
        expect(edgeKind(SUCCESS)).toBe('success')
        expect(edgeKind(FAILURE)).toBe('failure')
        expect(edgeKind('pr.merged')).toBe('event')
    })
})

describe('components/acp/flowDiagram', () => {
    test('draws every stage and labels the events it waits for', () => {
        render(wrapIntl(
            <FlowDiagram
                nodes={nodes}
                edges={edges}
                triggers={triggers}
            />,
        ))

        for (const node of nodes) {
            expect(screen.getByText(node.column)).toBeInTheDocument()
        }

        // The outcome arrows carry their colour, not a word; only the awaited
        // events are worth a label.
        expect(screen.getByText('ветка влита в основную')).toBeInTheDocument()
        expect(screen.queryByText('шаг прошёл')).not.toBeInTheDocument()
    })

    test('an empty route draws nothing at all', () => {
        const {container} = render(wrapIntl(
            <FlowDiagram
                nodes={[]}
                edges={[]}
                triggers={triggers}
            />,
        ))
        expect(container).toBeEmptyDOMElement()
    })
})

describe('components/acp/flowDiagram builder', () => {
    const waitTriggers = [
        {kind: 'branch.merged', source: 'git', label: 'ветка влита в основную'},
        {kind: 'pr.merged', source: 'github', label: 'pull request смержен'},
    ]

    test('the handle a connection is pulled from is what the transition means', () => {
        expect(connectEdge([], 'a', 'b', HANDLE_SUCCESS, waitTriggers)).toEqual([{from: 'a', to: 'b', on: SUCCESS}])
        expect(connectEdge([], 'a', 'b', HANDLE_FAILURE, waitTriggers)).toEqual([{from: 'a', to: 'b', on: FAILURE}])

        // An event connection takes the first trigger the stage does not
        // already wait for, so pulling twice does not overwrite the first.
        const first = connectEdge([], 'a', 'b', HANDLE_EVENT, waitTriggers)
        expect(first).toEqual([{from: 'a', to: 'b', on: 'branch.merged'}])
        expect(connectEdge(first, 'a', 'c', HANDLE_EVENT, waitTriggers)).toEqual([
            {from: 'a', to: 'b', on: 'branch.merged'},
            {from: 'a', to: 'c', on: 'pr.merged'},
        ])
    })

    test('a stage may have only one transition per event, and none to itself', () => {
        const existing = [{from: 'a', to: 'b', on: SUCCESS}]
        expect(connectEdge(existing, 'a', 'c', HANDLE_SUCCESS, waitTriggers)).toEqual([{from: 'a', to: 'c', on: SUCCESS}])
        expect(connectEdge(existing, 'a', 'a', HANDLE_SUCCESS, waitTriggers)).toBe(existing)
        expect(connectEdge(existing, '', 'c', HANDLE_SUCCESS, waitTriggers)).toBe(existing)
    })

    test('a stage placed by hand stays where it was put', () => {
        const placed = [
            {id: 'a', column: 'A', action: 'agent', x: 400, y: 40},
            {id: 'b', column: 'B', action: 'none'},
        ]
        const positions = layout(placed, [{from: 'a', to: 'b', on: SUCCESS}])
        expect(positions.get('a')).toEqual({x: 400, y: 40})

        // And one that was never placed is still laid out for the reader.
        expect(positions.get('b')).toBeDefined()
    })

    test('an editable canvas shows the outputs a route is drawn from', () => {
        const {container, rerender} = render(wrapIntl(
            <FlowDiagram
                nodes={nodes}
                edges={edges}
                triggers={triggers}
            />,
        ))
        expect(container.querySelector('.FlowDiagram--editable')).toBeNull()

        rerender(wrapIntl(
            <FlowDiagram
                nodes={nodes}
                edges={edges}
                triggers={triggers}
                onChange={jest.fn()}
            />,
        ))
        expect(container.querySelector('.FlowDiagram--editable')).not.toBeNull()
        expect(container.querySelectorAll('.FlowDiagram__out--success').length).toBe(nodes.length)
    })

    test('the map says how many cards stand on a stage', async () => {
        render(wrapIntl(
            <FlowDiagram
                nodes={nodes}
                edges={edges}
                triggers={triggers}
                counts={[{nodeId: 'agent', cards: 3, running: 1, queued: 1}]}
            />,
        ))
        expect(screen.getByText('3')).toBeInTheDocument()
    })
})
