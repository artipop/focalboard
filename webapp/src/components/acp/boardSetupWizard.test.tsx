// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.
import React from 'react'
import {render, screen, waitFor} from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import '@testing-library/jest-dom'

import {Board} from '../../blocks/board'
import {TestBlockFactory} from '../../test/testBlockFactory'
import {wrapIntl} from '../../testUtils'

import BoardSetupWizard, {isBoardSetupAvailable, readRegistry, rememberDismissed, setupNeeded} from './boardSetupWizard'

const anyWindow = window as any

// A board made from the template: it carries the routes it runs.
function templateBoard(): Board {
    const board = TestBlockFactory.createBoard()
    board.properties = {acpFlows: '[]'} as any
    return board
}

function stubBindings(overrides: Record<string, unknown> = {}) {
    const bindings = {
        ListAgentRepos: jest.fn().mockResolvedValue('[]'),
        ListAgents: jest.fn().mockResolvedValue('[]'),
        PickDirectory: jest.fn().mockResolvedValue('/Users/me/src/webapp'),
        AddAgentRepo: jest.fn().mockResolvedValue('{}'),
        AddAgent: jest.fn().mockResolvedValue('{}'),
        UpdateAgent: jest.fn().mockResolvedValue('{}'),
        SyncAgentUsers: jest.fn().mockResolvedValue('[]'),
        AddDeployTarget: jest.fn().mockResolvedValue('{}'),
        SeedBoardAutomation: jest.fn().mockResolvedValue(undefined),
        ...overrides,
    }
    anyWindow.go = {main: {App: bindings}}
    return bindings
}

describe('components/acp/boardSetupWizard when it offers itself', () => {
    afterEach(() => {
        delete anyWindow.go
        localStorage.clear()
        jest.clearAllMocks()
    })

    test('never outside the desktop app', async () => {
        expect(isBoardSetupAvailable()).toBe(false)
        expect(await readRegistry()).toBeNull()
        expect(setupNeeded(templateBoard(), {agents: [], repos: []})).toBe(false)
    })

    test('only for a board that runs something on a machine that cannot run it', () => {
        stubBindings()
        const empty = {agents: [], repos: []}
        expect(setupNeeded(templateBoard(), empty)).toBe(true)

        // A board that carries no routes has nothing to set up.
        expect(setupNeeded(TestBlockFactory.createBoard(), empty)).toBe(false)

        // And a machine that is already configured is not asked again.
        expect(setupNeeded(templateBoard(), {agents: [{name: 'claude'}], repos: [{name: 'webapp', path: '/src'}]})).toBe(false)

        // Half-configured still counts: an agent with no repository cannot work.
        expect(setupNeeded(templateBoard(), {agents: [{name: 'claude'}], repos: []})).toBe(true)
    })

    test('closing it is remembered for that board alone', () => {
        stubBindings()
        const board = templateBoard()
        const other = templateBoard()
        other.id = 'board-two'

        rememberDismissed(board.id)
        expect(setupNeeded(board, {agents: [], repos: []})).toBe(false)
        expect(setupNeeded(other, {agents: [], repos: []})).toBe(true)
    })
})

describe('components/acp/boardSetupWizard steps', () => {
    afterEach(() => {
        delete anyWindow.go
        localStorage.clear()
        jest.clearAllMocks()
    })

    const renderWizard = (onClose = jest.fn()) => render(wrapIntl(
        <BoardSetupWizard
            board={templateBoard()}
            onClose={onClose}
        />,
    ))

    test('the repository step will not pass until there is one', async () => {
        const bindings = stubBindings()
        renderWizard()
        await waitFor(() => expect(bindings.ListAgentRepos).toHaveBeenCalled())

        expect(screen.getByRole('button', {name: 'Next'})).toBeDisabled()

        userEvent.click(screen.getByRole('button', {name: 'Choose a folder…'}))
        await waitFor(() => expect(screen.getByDisplayValue('webapp')).toBeInTheDocument())

        userEvent.click(screen.getByRole('button', {name: 'Next'}))
        await waitFor(() => expect(bindings.AddAgentRepo).toHaveBeenCalledWith('webapp', '/Users/me/src/webapp'))

        // And having added it, the wizard is on the agent step.
        await waitFor(() => expect(screen.getByText('Kind')).toBeInTheDocument())
    })

    test('a refusal from Go is shown rather than swallowed', async () => {
        const bindings = stubBindings({AddAgentRepo: jest.fn().mockRejectedValue('/Users/me/src не является git-репозиторием')})
        renderWizard()
        await waitFor(() => expect(bindings.ListAgentRepos).toHaveBeenCalled())

        userEvent.click(screen.getByRole('button', {name: 'Choose a folder…'}))
        await waitFor(() => expect(screen.getByDisplayValue('webapp')).toBeInTheDocument())
        userEvent.click(screen.getByRole('button', {name: 'Next'}))

        await waitFor(() => expect(screen.getByText(/не является git-репозиторием/)).toBeInTheDocument())

        // And it stays on the step, rather than walking on past the problem.
        expect(screen.getByRole('button', {name: 'Choose a folder…'})).toBeInTheDocument()
    })

    test('an agent is registered and made assignable', async () => {
        const bindings = stubBindings({ListAgentRepos: jest.fn().mockResolvedValue(JSON.stringify([{name: 'webapp', path: '/src'}]))})
        renderWizard()
        await waitFor(() => expect(screen.getByRole('button', {name: 'Next'})).toBeEnabled())

        // The repository is already there, so this step is passed by moving on.
        userEvent.click(screen.getByRole('button', {name: 'Next'}))
        await waitFor(() => expect(screen.getByText('Kind')).toBeInTheDocument())

        userEvent.click(screen.getByRole('button', {name: 'Next'}))
        await waitFor(() => expect(bindings.AddAgent).toHaveBeenCalled())
        expect(JSON.parse(bindings.AddAgent.mock.calls[0][0])).toEqual({name: 'claude', kind: 'claude'})
        expect(bindings.SyncAgentUsers).toHaveBeenCalled()
    })

    test('deploy and testing are skippable, and the end takes the board’s automation', async () => {
        const bindings = stubBindings({
            ListAgentRepos: jest.fn().mockResolvedValue(JSON.stringify([{name: 'webapp', path: '/src'}])),
            ListAgents: jest.fn().mockResolvedValue(JSON.stringify([{name: 'claude'}])),
        })
        const onClose = jest.fn()
        renderWizard(onClose)
        await waitFor(() => expect(screen.getByRole('button', {name: 'Next'})).toBeEnabled())

        userEvent.click(screen.getByRole('button', {name: 'Next'}))
        await waitFor(() => expect(screen.getByText('Kind')).toBeInTheDocument())
        userEvent.click(screen.getByRole('button', {name: 'Next'}))

        await waitFor(() => expect(screen.getByText('Dokku host')).toBeInTheDocument())
        userEvent.click(screen.getByRole('button', {name: 'Skip'}))
        await waitFor(() => expect(screen.getByRole('button', {name: 'Skip'})).toBeInTheDocument())
        userEvent.click(screen.getByRole('button', {name: 'Skip'}))

        await waitFor(() => expect(screen.getByRole('button', {name: 'Done'})).toBeInTheDocument())
        expect(bindings.AddDeployTarget).not.toHaveBeenCalled()
        expect(bindings.UpdateAgent).not.toHaveBeenCalled()

        userEvent.click(screen.getByRole('button', {name: 'Done'}))
        await waitFor(() => expect(bindings.SeedBoardAutomation).toHaveBeenCalled())
        expect(onClose).toHaveBeenCalled()
    })

    test('the browser server is offered ready to accept', async () => {
        const bindings = stubBindings({
            ListAgentRepos: jest.fn().mockResolvedValue(JSON.stringify([{name: 'webapp', path: '/src'}])),
            ListAgents: jest.fn().mockResolvedValue(JSON.stringify([{name: 'claude'}])),
        })
        renderWizard()
        await waitFor(() => expect(screen.getByRole('button', {name: 'Next'})).toBeEnabled())
        userEvent.click(screen.getByRole('button', {name: 'Next'}))
        await waitFor(() => expect(screen.getByText('Kind')).toBeInTheDocument())
        userEvent.click(screen.getByRole('button', {name: 'Next'}))
        await waitFor(() => expect(screen.getByText('Dokku host')).toBeInTheDocument())
        userEvent.click(screen.getByRole('button', {name: 'Skip'}))

        await waitFor(() => expect(screen.getByText(/browser MCP server/)).toBeInTheDocument())
        userEvent.click(screen.getByRole('button', {name: 'Save'}))
        await waitFor(() => expect(bindings.UpdateAgent).toHaveBeenCalled())

        const saved = JSON.parse(bindings.UpdateAgent.mock.calls[0][0])
        expect(saved.name).toBe('claude')
        expect(saved.mcpServers.playwright.command).toBe('npx')
    })
})
