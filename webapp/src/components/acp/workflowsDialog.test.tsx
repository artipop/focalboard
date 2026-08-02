// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.
import React from 'react'
import {render, screen, waitFor, within} from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import '@testing-library/jest-dom'

import {Board, IPropertyTemplate} from '../../blocks/board'
import {TestBlockFactory} from '../../test/testBlockFactory'
import {wrapIntl} from '../../testUtils'
import {setupReactFlowEnvironment} from '../../test/reactFlowEnvironment'

import WorkflowsDialog, {isWorkflowsAvailable, edgeTarget, setEdge, waitEdges, SUCCESS, FAILURE} from './workflowsDialog'

// The editor draws the route on a React Flow canvas, which measures the page.
setupReactFlowEnvironment()

const anyWindow = window as any

// boardWithColumns gives the editor a Status property to offer columns from.
function boardWithColumns(): Board {
    const board = TestBlockFactory.createBoard()
    const status: IPropertyTemplate = {
        id: 'prop-status',
        name: 'Status',
        type: 'select',
        options: ['To Agent', 'Review', 'Deploy', 'Done'].map((value) => ({
            id: `opt-${value}`,
            value,
            color: 'propColorDefault',
        })),
    }
    board.cardProperties = [status]
    return board
}

const triggers = [
    {kind: 'success', source: 'outcome', label: 'шаг прошёл'},
    {kind: 'failure', source: 'outcome', label: 'шаг упал'},
    {kind: 'branch.merged', source: 'git', label: 'ветка влита в основную'},
    {kind: 'pr.merged', source: 'github', label: 'pull request смержен'},
]

const featureFlow = {
    name: 'feature',
    property: 'Status',
    nodes: [
        {id: 'n1', column: 'To Agent', action: 'agent'},
        {id: 'n2', column: 'Review', action: 'none'},
    ],
    edges: [{from: 'n1', to: 'n2', on: SUCCESS}],
}

// The routes the install ships with; the board template names the same ones.
const shippedFlows = [
    featureFlow,
    {
        name: 'Hotfix',
        property: 'Status',
        nodes: [
            {id: 'agent', column: 'To Agent', action: 'agent'},
            {id: 'deploy', column: 'Deploy', action: 'deploy'},
        ],
        edges: [{from: 'agent', to: 'deploy', on: SUCCESS}],
    },
]

function stubBindings(overrides: Record<string, unknown> = {}) {
    const bindings = {
        ListFlows: jest.fn().mockResolvedValue(JSON.stringify([featureFlow])),
        ListFlowTriggers: jest.fn().mockResolvedValue(JSON.stringify(triggers)),
        ListFlowTemplates: jest.fn().mockResolvedValue(JSON.stringify(shippedFlows)),
        GetBoardFlowOverview: jest.fn().mockResolvedValue(JSON.stringify([{
            flow: 'feature',
            cards: 2,
            stages: [
                {nodeId: 'n1', cards: 2, running: 1, queued: 0},
                {nodeId: 'n2', cards: 0, running: 0, queued: 0},
            ],
        }])),
        AddFlow: jest.fn().mockResolvedValue('{}'),
        UpdateFlow: jest.fn().mockResolvedValue('{}'),
        RemoveFlow: jest.fn().mockResolvedValue(undefined),
        ...overrides,
    }
    anyWindow.go = {main: {App: bindings}}
    return bindings
}

describe('components/acp/workflowsDialog', () => {
    afterEach(() => {
        delete anyWindow.go
        jest.clearAllMocks()
    })

    test('isWorkflowsAvailable is false without desktop bindings', () => {
        expect(isWorkflowsAvailable()).toBe(false)
    })

    test('lists the routes a board has', async () => {
        stubBindings()
        expect(isWorkflowsAvailable()).toBe(true)

        render(wrapIntl(
            <WorkflowsDialog
                board={boardWithColumns()}
                onClose={jest.fn()}
            />,
        ))

        await waitFor(() => expect(screen.getByText('feature')).toBeInTheDocument())

        // The route is drawn rather than spelled out, and it says where the
        // board's cards are standing on it.
        for (const stage of featureFlow.nodes) {
            expect(screen.getByText(stage.column)).toBeInTheDocument()
        }
        expect(screen.getByText('2 cards on this route')).toBeInTheDocument()

        // Two of them on the first stage, one of those working.
        expect(screen.getByText('2')).toBeInTheDocument()
    })

    test('edits a route: stages, outcomes and an awaited event', async () => {
        const bindings = stubBindings()
        render(wrapIntl(
            <WorkflowsDialog
                board={boardWithColumns()}
                onClose={jest.fn()}
            />,
        ))
        await waitFor(() => expect(screen.getByText('feature')).toBeInTheDocument())

        userEvent.click(screen.getByRole('button', {name: 'Edit'}))
        await waitFor(() => expect(screen.getByDisplayValue('feature')).toBeInTheDocument())

        // Columns come from the board, so a stage cannot point at one nobody has.
        const columnSelect = screen.getByDisplayValue('To Agent')
        expect(within(columnSelect).queryByText('Deploy')).toBeTruthy()

        // Add a third stage and wire the review stage to it on a merged branch.
        userEvent.click(screen.getByRole('button', {name: 'Add stage'}))
        await waitFor(() => expect(screen.getAllByRole('button', {name: 'Remove stage'})).toHaveLength(3))

        userEvent.click(screen.getAllByRole('button', {name: 'Wait for an event…'})[1])
        await waitFor(() => expect(screen.getByDisplayValue('ветка влита в основную')).toBeInTheDocument())

        userEvent.click(screen.getByRole('button', {name: 'Save'}))
        await waitFor(() => expect(bindings.UpdateFlow).toHaveBeenCalled())

        const saved = JSON.parse(bindings.UpdateFlow.mock.calls[0][0])
        expect(saved.name).toBe('feature')
        expect(saved.nodes).toHaveLength(3)
        expect(saved.edges).toEqual(expect.arrayContaining([
            {from: 'n1', to: 'n2', on: SUCCESS},
            expect.objectContaining({from: 'n2', on: 'branch.merged'}),
        ]))
    })

    test('a validation error from Go is shown, not swallowed', async () => {
        stubBindings({UpdateFlow: jest.fn().mockRejectedValue('у стадии "n1" два перехода по событию')})
        render(wrapIntl(
            <WorkflowsDialog
                board={boardWithColumns()}
                onClose={jest.fn()}
            />,
        ))
        await waitFor(() => expect(screen.getByText('feature')).toBeInTheDocument())

        userEvent.click(screen.getByRole('button', {name: 'Edit'}))
        await waitFor(() => expect(screen.getByDisplayValue('feature')).toBeInTheDocument())
        userEvent.click(screen.getByRole('button', {name: 'Save'}))

        await waitFor(() => expect(screen.getByText(/два перехода по событию/)).toBeInTheDocument())
    })

    // TODO(react-19): see docs/npm-dependency-warnings.md -- the Edit button appears only after a commit React 19 defers
    // eslint-disable-next-line no-only-tests/no-only-tests
    test.skip('asks for the routes of the board it was opened on, and saves them to it', async () => {
        const bindings = stubBindings()
        const board = boardWithColumns()
        render(wrapIntl(
            <WorkflowsDialog
                board={board}
                onClose={jest.fn()}
            />,
        ))
        await waitFor(() => expect(bindings.ListFlows).toHaveBeenCalledWith(board.id))

        userEvent.click(screen.getByRole('button', {name: 'Edit'}))
        await waitFor(() => expect(screen.getByDisplayValue('feature')).toBeInTheDocument())
        userEvent.click(screen.getByRole('button', {name: 'Save'}))

        await waitFor(() => expect(bindings.UpdateFlow).toHaveBeenCalled())
        expect(JSON.parse(bindings.UpdateFlow.mock.calls[0][0]).boardId).toBe(board.id)
    })

    test('offers the shipped routes the registry is missing, and only those', async () => {
        stubBindings()
        render(wrapIntl(
            <WorkflowsDialog
                board={boardWithColumns()}
                onClose={jest.fn()}
            />,
        ))
        await waitFor(() => expect(screen.getByText('feature')).toBeInTheDocument())

        // "feature" is already registered; only "Hotfix" is worth offering.
        expect(screen.queryByRole('button', {name: 'feature'})).toBeNull()
        userEvent.click(screen.getByRole('button', {name: 'Hotfix'}))

        // It opens in the editor rather than being saved behind the user's
        // back: the route is there to be looked at first.
        await waitFor(() => expect(screen.getByDisplayValue('Hotfix')).toBeInTheDocument())
        expect(screen.getAllByRole('button', {name: 'Remove stage'})).toHaveLength(2)
    })

    test('removing a route asks Go to forget it', async () => {
        const bindings = stubBindings()
        render(wrapIntl(
            <WorkflowsDialog
                board={boardWithColumns()}
                onClose={jest.fn()}
            />,
        ))
        await waitFor(() => expect(screen.getByText('feature')).toBeInTheDocument())

        userEvent.click(screen.getByRole('button', {name: 'Remove'}))
        await waitFor(() => expect(bindings.RemoveFlow).toHaveBeenCalledWith('feature'))
    })
})

describe('components/acp/workflowsDialog edges', () => {
    const edges = [
        {from: 'a', to: 'b', on: SUCCESS},
        {from: 'a', to: 'c', on: FAILURE},
        {from: 'b', to: 'c', on: 'pr.merged'},
    ]

    test('edgeTarget finds the stage an event leads to', () => {
        expect(edgeTarget(edges, 'a', SUCCESS)).toBe('b')
        expect(edgeTarget(edges, 'b', SUCCESS)).toBe('')
    })

    test('setEdge replaces a transition and an empty target removes it', () => {
        expect(edgeTarget(setEdge(edges, 'a', SUCCESS, 'c'), 'a', SUCCESS)).toBe('c')
        expect(setEdge(edges, 'a', SUCCESS, '')).toHaveLength(2)

        // A stage may have only one transition per event.
        expect(setEdge(edges, 'a', SUCCESS, 'c').filter((e) => e.from === 'a' && e.on === SUCCESS)).toHaveLength(1)
    })

    test('waitEdges are the awaited events only, not the stage outcomes', () => {
        expect(waitEdges(edges, 'a')).toHaveLength(0)
        expect(waitEdges(edges, 'b')).toEqual([{from: 'b', to: 'c', on: 'pr.merged'}])
    })
})
