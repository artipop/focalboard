// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.
import React from 'react'
import {render, screen, waitFor} from '@testing-library/react'
import '@testing-library/jest-dom'

import {wrapIntl} from '../../testUtils'

import FlowStrip, {isFlowStripAvailable, waited} from './flowStrip'

const anyWindow = window as any

const cardFlow = {
    flow: 'Feature',
    currentNodeId: 'review',
    since: new Date(Date.now() - (90 * 60 * 1000)).toISOString(),
    branch: 'feat/x',
    waitingFor: ['ветка влита в основную'],
    stages: [
        {nodeId: 'agent', column: 'In Progress', action: 'agent', crew: ['dev-1'], current: false, done: true},
        {nodeId: 'review', column: 'In Review', action: 'none', current: true, done: false},
        {nodeId: 'deploy', column: 'Deploy', action: 'deploy', current: false, done: false},
    ],
}

function stubBindings(flow: unknown) {
    const bindings = {GetCardFlow: jest.fn().mockResolvedValue(JSON.stringify(flow))}
    anyWindow.go = {main: {App: bindings}}
    return bindings
}

describe('components/acp/flowStrip', () => {
    afterEach(() => {
        delete anyWindow.go
        jest.clearAllMocks()
    })

    test('is unavailable without desktop bindings', () => {
        expect(isFlowStripAvailable()).toBe(false)
    })

    test('waited reports the wait in the units a person would say it in', () => {
        const now = Date.parse('2026-07-31T12:00:00Z')
        expect(waited('2026-07-31T11:40:00Z', now)).toEqual({value: 20, unit: 'minutes'})
        expect(waited('2026-07-31T09:00:00Z', now)).toEqual({value: 3, unit: 'hours'})
        expect(waited('2026-07-28T12:00:00Z', now)).toEqual({value: 3, unit: 'days'})
        expect(waited(undefined, now)).toBeNull()
        expect(waited('nonsense', now)).toBeNull()
    })

    test('shows the route, the stage the card is on and what it waits for', async () => {
        stubBindings(cardFlow)
        render(wrapIntl(<FlowStrip cardId='card1'/>))

        await waitFor(() => expect(screen.getByText('Feature')).toBeInTheDocument())
        expect(screen.getByText('feat/x')).toBeInTheDocument()
        for (const stage of cardFlow.stages) {
            expect(screen.getByText(stage.column)).toBeInTheDocument()
        }

        // The current stage is marked, so the strip answers "where is it" at a glance.
        expect(screen.getByText('In Review')).toHaveClass('FlowStrip__stage--current')
        expect(screen.getByText('In Progress')).toHaveClass('FlowStrip__stage--done')

        // And the one question a stalled card raises is answered outright,
        // together with how long it has been standing there.
        expect(screen.getByText(/ветка влита в основную/).textContent).toMatch(/1 h/)
    })

    test('a card waiting for a place in the column says so', async () => {
        stubBindings({...cardFlow, waitingFor: [], queued: true})
        render(wrapIntl(<FlowStrip cardId='card1'/>))
        await waitFor(() => expect(screen.getByText(/free place in the column/)).toBeInTheDocument())
    })

    test('a card with no route draws nothing', async () => {
        stubBindings(null)
        const {container} = render(wrapIntl(<FlowStrip cardId='card1'/>))
        await waitFor(() => expect(container).toBeEmptyDOMElement())
    })
})
