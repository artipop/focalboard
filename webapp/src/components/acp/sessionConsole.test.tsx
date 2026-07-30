// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.
import React from 'react'
import {render, screen, waitFor} from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import '@testing-library/jest-dom'

import {wrapIntl} from '../../testUtils'

import SessionConsole, {isSessionConsoleAvailable} from './sessionConsole'

const anyWindow = window as any

// emitters collects the Wails event handlers the console subscribes to, so a
// test can push a live event into it.
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

function bindingsWith(sessions: any[], events: any[] = []) {
    return {
        GetCardSessions: jest.fn().mockResolvedValue(JSON.stringify({sessions, events})),
        StartCardSession: jest.fn().mockResolvedValue('sess-new'),
        ListAgentRepos: jest.fn().mockResolvedValue(JSON.stringify([{name: 'NotBro'}, {name: 'leadheat'}])),
        PromptSession: jest.fn().mockResolvedValue(undefined),
        AnswerPermission: jest.fn().mockResolvedValue(undefined),
        AttachSession: jest.fn().mockResolvedValue(true),
        DetachSession: jest.fn(),
        CloseSession: jest.fn().mockResolvedValue(undefined),
        CancelSession: jest.fn().mockResolvedValue(true),
        AnswerQuestion: jest.fn().mockResolvedValue(undefined),
        StartCardDeploy: jest.fn().mockResolvedValue('sess-deploy'),
    }
}

describe('components/acp/sessionConsole', () => {
    afterEach(() => {
        delete anyWindow.go
        delete anyWindow.runtime
        jest.clearAllMocks()
    })

    test('is inert without desktop bindings', () => {
        expect(isSessionConsoleAvailable()).toBe(false)
        const {container} = render(wrapIntl(<SessionConsole cardId='card1'/>))
        expect(container).toBeEmptyDOMElement()
    })

    test('replays a finished session and offers to open a new one', async () => {
        const bindings = bindingsWith(
            [{id: 'sess-1', status: 'done'}],
            [
                {sessionId: 'sess-1', kind: 'prompt', payload: {text: 'fix the bug'}},
                {sessionId: 'sess-1', kind: 'chunk', payload: {text: 'Looking'}},
                {sessionId: 'sess-1', kind: 'chunk', payload: {text: ' into it.'}},
                {sessionId: 'sess-1', kind: 'tool_call', payload: {toolCallId: 't1', title: 'Read src/app.go', status: 'pending'}},
            ],
        )
        anyWindow.go = {main: {App: bindings}}
        expect(isSessionConsoleAvailable()).toBe(true)

        render(wrapIntl(<SessionConsole cardId='card1'/>))

        // Consecutive chunks are merged into one paragraph.
        await waitFor(() => expect(screen.getByText('Looking into it.')).toBeInTheDocument())
        expect(screen.getByText('fix the bug')).toBeInTheDocument()
        expect(screen.getByText('Read src/app.go')).toBeInTheDocument()

        // A terminal session is not attached to and offers a fresh one.
        expect(bindings.AttachSession).not.toBeCalled()
        userEvent.click(screen.getByRole('button', {name: 'Open session'}))
        await waitFor(() => expect(bindings.StartCardSession).toBeCalledWith('card1', ''))
    })

    test('attaches to a live session and sends a follow-up turn', async () => {
        const bindings = bindingsWith([{id: 'sess-2', status: 'idle'}])
        anyWindow.go = {main: {App: bindings}}
        fakeRuntime()

        render(wrapIntl(<SessionConsole cardId='card1'/>))
        await waitFor(() => expect(bindings.AttachSession).toBeCalledWith('sess-2'))

        const box = screen.getByPlaceholderText(/Message the agent/)
        userEvent.type(box, 'also update the docs')
        userEvent.click(screen.getByRole('button', {name: 'Send'}))

        await waitFor(() => expect(bindings.PromptSession).toBeCalledWith('sess-2', 'also update the docs'))
    })

    test('answers a permission prompt pushed over the event bus', async () => {
        const bindings = bindingsWith([{id: 'sess-3', status: 'running'}])
        anyWindow.go = {main: {App: bindings}}
        const handlers = fakeRuntime()

        render(wrapIntl(<SessionConsole cardId='card1'/>))
        await waitFor(() => expect(bindings.AttachSession).toBeCalledWith('sess-3'))

        handlers['acp:permission']({
            cardId: 'card1',
            sessionId: 'sess-3',
            requestId: 'req-1',
            tool: 'Bash',
            title: 'Bash: rm -rf build',
            pending: true,
            options: [
                {optionId: 'allow', name: 'Allow once', kind: 'allow_once'},
                {optionId: 'reject', name: 'Reject', kind: 'reject_once'},
            ],
        })

        await waitFor(() => expect(screen.getByText('Bash: rm -rf build')).toBeInTheDocument())
        userEvent.click(screen.getByRole('button', {name: 'Allow once'}))
        await waitFor(() => expect(bindings.AnswerPermission).toBeCalledWith('sess-3', 'req-1', 'allow'))
    })

    test('attaches to a session that starts while the card is already open', async () => {
        // No session yet when the console mounts: the card was opened first and
        // the agent was triggered afterwards. Without attaching here the backend
        // sees nobody watching and answers permission prompts by policy.
        const bindings = bindingsWith([])
        anyWindow.go = {main: {App: bindings}}
        const handlers = fakeRuntime()

        render(wrapIntl(<SessionConsole cardId='card1'/>))
        await waitFor(() => expect(bindings.GetCardSessions).toBeCalled())
        expect(bindings.AttachSession).not.toBeCalled()

        handlers['acp:session']({cardId: 'card1', sessionId: 'sess-late', status: 'running'})
        await waitFor(() => expect(bindings.AttachSession).toBeCalledWith('sess-late'))

        // Terminal status releases the attachment again.
        handlers['acp:session']({cardId: 'card1', sessionId: 'sess-late', status: 'done'})
        await waitFor(() => expect(bindings.DetachSession).toBeCalledWith('sess-late'))
    })

    test('renders the agent answer as markdown, not as source', async () => {
        const bindings = bindingsWith(
            [{id: 'sess-md', status: 'done'}],
            [{sessionId: 'sess-md', kind: 'chunk', payload: {text: 'Правим **store.ts**:\n\n- кеш\n- инвалидация\n'}}],
        )
        anyWindow.go = {main: {App: bindings}}

        const {container} = render(wrapIntl(<SessionConsole cardId='card1'/>))

        await waitFor(() => expect(container.querySelector('strong')).toBeInTheDocument())
        expect(container.querySelector('strong')).toHaveTextContent('store.ts')
        expect(container.querySelectorAll('li')).toHaveLength(2)
        expect(screen.queryByText(/\*\*store\.ts\*\*/)).not.toBeInTheDocument()
    })

    test('surfaces a failed follow-up turn', async () => {
        const bindings = bindingsWith([{id: 'sess-6', status: 'running'}])
        anyWindow.go = {main: {App: bindings}}
        const handlers = fakeRuntime()

        render(wrapIntl(<SessionConsole cardId='card1'/>))
        await waitFor(() => expect(bindings.AttachSession).toBeCalled())

        // The session stays live, so only this event tells the user it failed.
        handlers['acp:session']({
            cardId: 'card1',
            sessionId: 'sess-6',
            status: 'idle',
            error: 'session/prompt: agent exited',
        })
        await waitFor(() => expect(screen.getByText('session/prompt: agent exited')).toBeInTheDocument())
    })

    test('answers the agent own questions through the picker', async () => {
        const bindings = bindingsWith([{id: 'sess-q', status: 'running'}])
        anyWindow.go = {main: {App: bindings}}
        const handlers = fakeRuntime()

        render(wrapIntl(<SessionConsole cardId='card1'/>))
        await waitFor(() => expect(bindings.AttachSession).toBeCalledWith('sess-q'))

        handlers['acp:question']({
            cardId: 'card1',
            sessionId: 'sess-q',
            requestId: 'q-1',
            questions: [{
                question: 'Какая цель рефакторинга?',
                header: 'Цель',
                multiSelect: false,
                options: [
                    {label: 'Читаемость', description: 'структура'},
                    {label: 'Скорость', description: 'перф'},
                ],
            }],
        })

        await waitFor(() => expect(screen.getByText('Какая цель рефакторинга?')).toBeInTheDocument())
        expect(screen.getByText('структура')).toBeInTheDocument()

        // Nothing is sent until something is picked.
        expect(screen.getByRole('button', {name: 'Ответить'})).toBeDisabled()

        userEvent.click(screen.getByText('Читаемость'))
        userEvent.click(screen.getByRole('button', {name: 'Ответить'}))

        await waitFor(() => expect(bindings.AnswerQuestion).toBeCalled())
        const [sessionId, requestId, text] = bindings.AnswerQuestion.mock.calls[0]
        expect(sessionId).toBe('sess-q')
        expect(requestId).toBe('q-1')
        expect(text).toContain('Цель: Читаемость')
    })

    test('offers a repository when the card does not name one', async () => {
        const bindings = bindingsWith([{id: 'sess-old', status: 'done'}])
        bindings.StartCardSession = jest.fn().
            mockRejectedValueOnce(new Error('ни тег карточки, ни исходная колонка не совпали с репозиторием из реестра (NotBro, leadheat)')).
            mockResolvedValueOnce('sess-picked')
        anyWindow.go = {main: {App: bindings}}

        render(wrapIntl(<SessionConsole cardId='card1'/>))
        userEvent.click(await screen.findByRole('button', {name: 'Open session'}))

        // The dead end becomes a choice instead of just an error.
        const select = await screen.findByRole('combobox')
        userEvent.selectOptions(select, 'leadheat')
        userEvent.click(screen.getByRole('button', {name: 'Open here'}))

        await waitFor(() => expect(bindings.StartCardSession).toHaveBeenLastCalledWith('card1', 'leadheat'))
    })

    test('ignores events belonging to other cards', async () => {
        const bindings = bindingsWith([{id: 'sess-4', status: 'running'}])
        anyWindow.go = {main: {App: bindings}}
        const handlers = fakeRuntime()

        render(wrapIntl(<SessionConsole cardId='card1'/>))
        await waitFor(() => expect(bindings.AttachSession).toBeCalled())

        handlers['acp:chunk']({cardId: 'other-card', sessionId: 'sess-9', text: 'not mine'})
        expect(screen.queryByText('not mine')).not.toBeInTheDocument()
    })

    test('detaches when the card closes', async () => {
        const bindings = bindingsWith([{id: 'sess-5', status: 'idle'}])
        anyWindow.go = {main: {App: bindings}}

        const {unmount} = render(wrapIntl(<SessionConsole cardId='card1'/>))
        await waitFor(() => expect(bindings.AttachSession).toBeCalledWith('sess-5'))

        unmount()
        expect(bindings.DetachSession).toBeCalledWith('sess-5')
    })

    test('shows the session branch and deploys it', async () => {
        const handlers = fakeRuntime()
        const bindings = bindingsWith([{id: 'sess-1', status: 'running'}])
        anyWindow.go = {main: {App: bindings}}

        render(wrapIntl(<SessionConsole cardId='card1'/>))
        await waitFor(() => expect(bindings.GetCardSessions).toBeCalled())

        // A session without a worktree yet has no branch to show or publish.
        expect(screen.queryByRole('button', {name: 'Deploy'})).not.toBeInTheDocument()

        // The branch arrives with the event the worktree creation emits.
        handlers['acp:session']({
            sessionId: 'sess-1',
            cardId: 'card1',
            status: 'running',
            branch: 'acp/login-via-sso-1a2b3c4d',
            worktreePath: '/tmp/wt/repo-1a2b3c4d',
        })
        await waitFor(() => expect(screen.getByText('acp/login-via-sso-1a2b3c4d')).toBeInTheDocument())

        userEvent.click(screen.getByRole('button', {name: 'Deploy'}))
        await waitFor(() => expect(bindings.StartCardDeploy).toBeCalledWith('card1', 'acp/login-via-sso-1a2b3c4d'))
    })

    test('a deploy session reports separately and does not take over the console', async () => {
        const handlers = fakeRuntime()
        const bindings = bindingsWith([{id: 'sess-1', status: 'running', branch: 'acp/task-1a2b3c4d'}])
        anyWindow.go = {main: {App: bindings}}

        render(wrapIntl(<SessionConsole cardId='card1'/>))
        await waitFor(() => expect(screen.getByText('acp/task-1a2b3c4d')).toBeInTheDocument())

        handlers['acp:session']({sessionId: 'sess-deploy', cardId: 'card1', status: 'running', deploy: true, branch: 'acp/task-1a2b3c4d'})
        handlers['acp:chunk']({sessionId: 'sess-deploy', cardId: 'card1', text: 'pushing to dokku'})

        await waitFor(() => expect(screen.getByText(/deploy: running/)).toBeInTheDocument())

        // The card's own session is still the one being attached to and shown.
        expect(screen.queryByText('pushing to dokku')).not.toBeInTheDocument()
        expect(bindings.AttachSession).not.toBeCalledWith('sess-deploy')
    })

    test('surfaces a failed deploy start', async () => {
        fakeRuntime()
        const bindings = bindingsWith([{id: 'sess-1', status: 'running', branch: 'acp/task-1a2b3c4d'}])
        bindings.StartCardDeploy = jest.fn().mockRejectedValue(new Error('не настроено ни одной цели деплоя'))
        anyWindow.go = {main: {App: bindings}}

        render(wrapIntl(<SessionConsole cardId='card1'/>))
        await waitFor(() => expect(screen.getByRole('button', {name: 'Deploy'})).toBeInTheDocument())

        userEvent.click(screen.getByRole('button', {name: 'Deploy'}))
        await waitFor(() => expect(screen.getByText(/не настроено ни одной цели/)).toBeInTheDocument())
    })
})
