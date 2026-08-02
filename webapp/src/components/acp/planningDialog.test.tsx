// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.
import React from 'react'
import {render, screen, waitFor} from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import '@testing-library/jest-dom'

import {wrapIntl} from '../../testUtils'
import {createBoard} from '../../blocks/board'
import {createBoardView} from '../../blocks/boardView'
import mutator from '../../mutator'

import PlanningDialog, {isPlanningAvailable} from './planningDialog'

jest.mock('../../mutator')
jest.mock('../../octoClient')

const anyWindow = window as any
const mockedMutator = jest.mocked(mutator, true)

function fakeRuntime() {
    const handlers: {[event: string]: (payload: any) => void} = {}
    anyWindow.runtime = {
        EventsOn: (event: string, cb: (payload: any) => void) => {
            handlers[event] = cb
            return () => delete handlers[event]
        },
    }
    return handlers
}

function planningBindings() {
    return {
        ListAgentRepos: jest.fn().mockResolvedValue(JSON.stringify([{name: 'app', path: '/src/app'}])),
        ListAgents: jest.fn().mockResolvedValue(JSON.stringify([{name: 'planner', kind: 'claude'}])),
        StartPlanningSession: jest.fn().mockResolvedValue('plan-1'),
        ComposeTask: jest.fn().mockResolvedValue('Кэшировать список досок\nДобавить кеш в store.'),
        PromptSession: jest.fn().mockResolvedValue(undefined),
        AnswerPermission: jest.fn().mockResolvedValue(undefined),
        CloseSession: jest.fn().mockResolvedValue(undefined),
    }
}

// A board grouped by Status, whose leftmost column is "Backlog".
function boardAndView() {
    const board = createBoard()
    board.cardProperties = [{
        id: 'status',
        name: 'Status',
        type: 'select',
        options: [
            {id: 'backlog', value: 'Backlog', color: ''},
            {id: 'doing', value: 'Doing', color: ''},
        ],
    }]
    const view = createBoardView()
    view.fields.groupById = 'status'
    return {board, view}
}

describe('components/acp/planningDialog', () => {
    beforeEach(() => {
        mockedMutator.performAsUndoGroup.mockImplementation(async (fn: any) => fn())
        mockedMutator.insertBlock.mockImplementation(async (_boardId: string, block: any) => ({...block, id: block.id || 'new-block'}))
    })

    afterEach(() => {
        delete anyWindow.go
        delete anyWindow.runtime
        jest.clearAllMocks()
    })

    test('is inert without desktop bindings', () => {
        expect(isPlanningAvailable()).toBe(false)
        const {board, view} = boardAndView()
        const {container} = render(wrapIntl(
            <PlanningDialog
                board={board}
                activeView={view}
                onClose={jest.fn()}
            />,
        ))
        expect(container).toBeEmptyDOMElement()
    })

    test('preselects a registered repository and agent, then starts a session', async () => {
        const bindings = planningBindings()
        anyWindow.go = {main: {App: bindings}}
        fakeRuntime()
        expect(isPlanningAvailable()).toBe(true)

        const {board, view} = boardAndView()
        render(wrapIntl(
            <PlanningDialog
                board={board}
                activeView={view}
                onClose={jest.fn()}
            />,
        ))

        // A registered repository is selected by default, so Start is live at
        // once and the session is not accidentally code-blind.
        const start = await screen.findByRole('button', {name: 'Start planning'})
        await waitFor(() => expect(start).toBeEnabled())
        userEvent.click(start)

        await waitFor(() => expect(bindings.StartPlanningSession).toHaveBeenCalledWith('app', 'planner'))
    })

    test('plans without a repository when one is not chosen', async () => {
        const bindings = planningBindings()
        anyWindow.go = {main: {App: bindings}}
        fakeRuntime()

        const {board, view} = boardAndView()
        render(wrapIntl(
            <PlanningDialog
                board={board}
                activeView={view}
                onClose={jest.fn()}
            />,
        ))

        // The lone repository is preselected, so opting out has to be explicit.
        const repoSelect = await screen.findByDisplayValue('app')
        userEvent.selectOptions(repoSelect, '')

        const start = screen.getByRole('button', {name: 'Start planning'})
        expect(start).toBeEnabled()
        userEvent.click(start)

        await waitFor(() => expect(bindings.StartPlanningSession).toHaveBeenCalledWith('', 'planner'))
    })

    test('composes a task, lets it be edited, and creates the card in the first column', async () => {
        const bindings = planningBindings()
        anyWindow.go = {main: {App: bindings}}
        const handlers = fakeRuntime()
        const onClose = jest.fn()

        const {board, view} = boardAndView()
        render(wrapIntl(
            <PlanningDialog
                board={board}
                activeView={view}
                onClose={onClose}
            />,
        ))

        userEvent.click(await screen.findByRole('button', {name: 'Start planning'}))
        await waitFor(() => expect(bindings.StartPlanningSession).toHaveBeenCalled())

        // The session announces itself and says something, which is what makes
        // "Create task" meaningful.
        handlers['acp:session']({sessionId: 'plan-1', status: 'idle'})
        handlers['acp:chunk']({sessionId: 'plan-1', text: 'Что именно кешируем?'})
        await waitFor(() => expect(screen.getByText('Что именно кешируем?')).toBeInTheDocument())

        userEvent.click(screen.getByRole('button', {name: 'Create task'}))
        await waitFor(() => expect(bindings.ComposeTask).toHaveBeenCalledWith('plan-1'))

        // The answer is split into a title and a body for review, not written
        // straight to the board.
        const title = await screen.findByDisplayValue('Кэшировать список досок')
        expect(screen.getByDisplayValue('Добавить кеш в store.')).toBeInTheDocument()
        expect(mockedMutator.insertBlock).not.toHaveBeenCalled()

        userEvent.type(title, ' (v2)')
        userEvent.click(screen.getByRole('button', {name: 'Create card'}))

        await waitFor(() => expect(mockedMutator.insertBlock).toHaveBeenCalled())
        const card = mockedMutator.insertBlock.mock.calls[0][1] as any
        expect(card.title).toBe('Кэшировать список досок (v2)')
        expect(card.fields.properties.status).toBe('backlog')
        await waitFor(() => expect(onClose).toHaveBeenCalled())
    })

    test('closes the session when the dialog goes away', async () => {
        const bindings = planningBindings()
        anyWindow.go = {main: {App: bindings}}
        fakeRuntime()

        const {board, view} = boardAndView()
        const {unmount} = render(wrapIntl(
            <PlanningDialog
                board={board}
                activeView={view}
                onClose={jest.fn()}
            />,
        ))

        userEvent.click(await screen.findByRole('button', {name: 'Start planning'}))
        await waitFor(() => expect(bindings.StartPlanningSession).toHaveBeenCalled())

        unmount()
        expect(bindings.CloseSession).toHaveBeenCalledWith('plan-1')
    })
})
