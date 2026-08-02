// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.
import React from 'react'
import {fireEvent, render, screen, waitFor} from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import '@testing-library/jest-dom'

import {wrapIntl} from '../../testUtils'
import {TestBlockFactory} from '../../test/testBlockFactory'
import mutator from '../../mutator'

import AgentsDialog, {isAgentsAvailable, textToServers} from './agentsDialog'

jest.mock('../../mutator')
const mockedMutator = jest.mocked(mutator)

const anyWindow = window as any

describe('components/acp/agentsDialog', () => {
    const board = TestBlockFactory.createBoard()

    afterEach(() => {
        delete anyWindow.go
        jest.clearAllMocks()
    })

    test('isAgentsAvailable is false without desktop bindings', () => {
        expect(isAgentsAvailable()).toBe(false)
    })

    test('lists agents and adds a codex agent with env', async () => {
        const bindings = {
            ListAgents: jest.fn().mockResolvedValue(JSON.stringify([{name: 'default-agent', kind: 'claude'}])),
            GetAgentSystemPrompt: jest.fn().mockResolvedValue(''),
            SetAgentSystemPrompt: jest.fn().mockResolvedValue(undefined),
            AddAgent: jest.fn().mockResolvedValue(JSON.stringify({name: 'codex-a', kind: 'codex'})),
            UpdateAgent: jest.fn(),
            RemoveAgent: jest.fn(),
        }
        anyWindow.go = {main: {App: bindings}}
        expect(isAgentsAvailable()).toBe(true)

        render(wrapIntl(
            <AgentsDialog
                board={board}
                onClose={jest.fn()}
            />,
        ))
        await waitFor(() => expect(screen.getByText('default-agent')).toBeInTheDocument())

        userEvent.click(screen.getByRole('button', {name: 'Add agent…'}))

        // Two selects in the form: the agent kind, then the proxy configuration.
        await waitFor(() => expect(screen.getAllByRole('combobox')).toHaveLength(2))

        userEvent.selectOptions(screen.getAllByRole('combobox')[0], 'codex')
        userEvent.type(screen.getByPlaceholderText('Name (matches the "Agent" option)'), 'codex-a')
        userEvent.type(screen.getByPlaceholderText('CODEX_HOME=/Users/me/.codex-work'), 'CODEX_HOME=/tmp/x')

        userEvent.click(screen.getByRole('button', {name: 'Save'}))
        await waitFor(() => expect(bindings.AddAgent).toHaveBeenCalled())
        const payload = JSON.parse(bindings.AddAgent.mock.calls[0][0])
        expect(payload).toMatchObject({name: 'codex-a', kind: 'codex', env: {CODEX_HOME: '/tmp/x'}})
    })

    test('saves a wrapped launch command and picks a registered proxy configuration', async () => {
        const bindings = {
            ListAgents: jest.fn().mockResolvedValue(JSON.stringify([])),
            ListProxies: jest.fn().mockResolvedValue(JSON.stringify([
                {name: 'office', proxy: 'http://proxy.example.com:8080'},
            ])),
            GetAgentSystemPrompt: jest.fn().mockResolvedValue(''),
            SetAgentSystemPrompt: jest.fn(),
            AddAgent: jest.fn().mockResolvedValue(JSON.stringify({name: 'proxied', kind: 'claude'})),
            UpdateAgent: jest.fn(),
            RemoveAgent: jest.fn(),
        }
        anyWindow.go = {main: {App: bindings}}

        render(wrapIntl(
            <AgentsDialog
                board={board}
                onClose={jest.fn()}
            />,
        ))
        await waitFor(() => expect(screen.getByRole('button', {name: 'Add agent…'})).toBeInTheDocument())

        userEvent.click(screen.getByRole('button', {name: 'Add agent…'}))
        await waitFor(() => expect(screen.getAllByRole('combobox')).toHaveLength(2))

        userEvent.type(screen.getByPlaceholderText('Name (matches the "Agent" option)'), 'proxied')

        // The launch command is offered for claude too, and quoted arguments
        // stay a single argv element.
        userEvent.type(screen.getByPlaceholderText('proxychains4 -q -f /etc/myproxy.conf claude-agent-acp'), 'proxychains4 -f "/etc/my conf.conf" claude-agent-acp')

        // The network settings themselves live in the proxy registry; the agent
        // only names one.
        userEvent.selectOptions(screen.getAllByRole('combobox')[1], 'office')

        userEvent.click(screen.getByRole('button', {name: 'Save'}))
        await waitFor(() => expect(bindings.AddAgent).toHaveBeenCalled())
        const payload = JSON.parse(bindings.AddAgent.mock.calls[0][0])
        expect(payload).toMatchObject({
            name: 'proxied',
            kind: 'claude',
            command: ['proxychains4', '-f', '/etc/my conf.conf', 'claude-agent-acp'],
            proxyName: 'office',
        })
    })

    // Two kinds are reached through an adapter published on npm, so whether one
    // is installed is knowable here — and the alternative is a card failing an
    // hour later with nobody watching.
    test('says when the kind cannot be started and offers to install it', async () => {
        const bindings = {
            ListAgents: jest.fn().mockResolvedValue(JSON.stringify([])),
            GetAgentSystemPrompt: jest.fn().mockResolvedValue(''),
            SetAgentSystemPrompt: jest.fn(),
            ListAgentAdapters: jest.fn().mockResolvedValue(JSON.stringify([
                {kind: 'claude', package: '@agentclientprotocol/claude-agent-acp', ready: false, detail: 'не найден claude-agent-acp'},
                {kind: 'codex', package: '@agentclientprotocol/codex-acp', ready: true, path: '/usr/local/bin/codex-acp'},
            ])),
            InstallAgentAdapter: jest.fn().mockResolvedValue('added 1 package'),
            AddAgent: jest.fn(),
            UpdateAgent: jest.fn(),
            RemoveAgent: jest.fn(),
        }
        anyWindow.go = {main: {App: bindings}}

        render(wrapIntl(
            <AgentsDialog
                board={board}
                onClose={jest.fn()}
            />,
        ))
        await waitFor(() => expect(bindings.ListAgentAdapters).toHaveBeenCalled())

        userEvent.click(screen.getByRole('button', {name: 'Add agent…'}))

        // The default kind is claude, which this machine cannot start.
        await waitFor(() => expect(screen.getByText('не найден claude-agent-acp')).toBeInTheDocument())

        userEvent.click(screen.getByRole('button', {name: 'Install adapter'}))
        await waitFor(() => expect(bindings.InstallAgentAdapter).toHaveBeenCalledWith('claude'))

        // An installed kind says nothing at all.
        userEvent.selectOptions(screen.getAllByRole('combobox')[0], 'codex')
        await waitFor(() => expect(screen.queryByText('не найден claude-agent-acp')).not.toBeInTheDocument())
    })

    test('offers the ACP-native kinds and saves one without a launch command', async () => {
        const bindings = {
            ListAgents: jest.fn().mockResolvedValue(JSON.stringify([])),
            GetAgentSystemPrompt: jest.fn().mockResolvedValue(''),
            SetAgentSystemPrompt: jest.fn(),
            AddAgent: jest.fn().mockResolvedValue(JSON.stringify({name: 'junie-a', kind: 'junie'})),
            UpdateAgent: jest.fn(),
            RemoveAgent: jest.fn(),
        }
        anyWindow.go = {main: {App: bindings}}

        render(wrapIntl(
            <AgentsDialog
                board={board}
                onClose={jest.fn()}
            />,
        ))
        await waitFor(() => expect(screen.getByRole('button', {name: 'Add agent…'})).toBeInTheDocument())

        userEvent.click(screen.getByRole('button', {name: 'Add agent…'}))
        await waitFor(() => expect(screen.getAllByRole('combobox')).toHaveLength(2))

        const kindSelect = screen.getAllByRole('combobox')[0]
        expect(Array.from(kindSelect.querySelectorAll('option')).map((o) => o.value)).
            toEqual(['claude', 'codex', 'antigravity', 'copilot', 'junie', 'acp'])

        userEvent.selectOptions(kindSelect, 'junie')
        userEvent.type(screen.getByPlaceholderText('Name (matches the "Agent" option)'), 'junie-a')

        // The kind carries its own default launch flags, so the command input
        // only shows them as a placeholder and stays empty.
        expect(screen.getByPlaceholderText('junie --acp=true')).toHaveValue('')

        userEvent.click(screen.getByRole('button', {name: 'Save'}))
        await waitFor(() => expect(bindings.AddAgent).toHaveBeenCalled())
        const payload = JSON.parse(bindings.AddAgent.mock.calls[0][0])
        expect(payload).toMatchObject({name: 'junie-a', kind: 'junie', command: []})
    })

    test('keeps every agent assignable without being asked', async () => {
        const bindings = {
            ListAgents: jest.fn().mockResolvedValue(JSON.stringify([{name: 'Codex Acct1', kind: 'codex'}])),
            GetAgentSystemPrompt: jest.fn().mockResolvedValue(''),
            SetAgentSystemPrompt: jest.fn(),
            AddAgent: jest.fn().mockResolvedValue(JSON.stringify({name: 'claude', kind: 'claude'})),
            UpdateAgent: jest.fn(),
            RemoveAgent: jest.fn().mockResolvedValue(undefined),
            SyncAgentUsers: jest.fn().mockResolvedValue(JSON.stringify([
                {name: 'Codex Acct1', username: 'codex-acct1', userId: 'uid-1', created: true},
            ])),
        }
        anyWindow.go = {main: {App: bindings}}

        render(wrapIntl(
            <AgentsDialog
                board={board}
                onClose={jest.fn()}
            />,
        ))

        // Opening the dialog is enough: the accounts and board memberships are
        // provisioned for the board being looked at.
        await waitFor(() => expect(bindings.SyncAgentUsers).toHaveBeenCalledWith(board.id))
        await waitFor(() => expect(screen.getByText('Codex Acct1')).toBeInTheDocument())

        // And a newly registered agent is provisioned right after it is saved.
        userEvent.click(screen.getByRole('button', {name: 'Add agent…'}))
        await waitFor(() => expect(screen.getAllByRole('combobox')).toHaveLength(2))
        userEvent.type(screen.getByPlaceholderText('Name (matches the "Agent" option)'), 'claude')
        userEvent.click(screen.getByRole('button', {name: 'Save'}))

        await waitFor(() => expect(bindings.AddAgent).toHaveBeenCalled())
        await waitFor(() => expect(bindings.SyncAgentUsers).toHaveBeenCalledTimes(2))
    })

    test('carries the proxy registry in a folded-away section', async () => {
        const bindings = {
            ListAgents: jest.fn().mockResolvedValue(JSON.stringify([{name: 'codex-a', kind: 'codex'}])),
            ListProxies: jest.fn().mockResolvedValue(JSON.stringify([{name: 'office', proxy: 'http://proxy.example.com:8080'}])),
            GetAgentSystemPrompt: jest.fn().mockResolvedValue(''),
            SetAgentSystemPrompt: jest.fn(),
            AddAgent: jest.fn(),
            UpdateAgent: jest.fn(),
            RemoveAgent: jest.fn(),
            AddProxy: jest.fn(),
            UpdateProxy: jest.fn(),
            RemoveProxy: jest.fn(),
        }
        anyWindow.go = {main: {App: bindings}}

        render(wrapIntl(
            <AgentsDialog
                board={board}
                onClose={jest.fn()}
            />,
        ))
        await waitFor(() => expect(screen.getByText('codex-a')).toBeInTheDocument())

        // The proxies live behind a summary, in the dialog that references them.
        const proxies = screen.getByText('Proxy configurations')
        expect(proxies.tagName.toLowerCase()).toBe('summary')
        await waitFor(() => expect(screen.getByText('office')).toBeInTheDocument())
        expect(screen.getByRole('button', {name: 'Add configuration…'})).toBeInTheDocument()
    })

    test('shows the registry even when provisioning fails', async () => {
        const bindings = {
            ListAgents: jest.fn().mockResolvedValue(JSON.stringify([{name: 'codex-a', kind: 'codex'}])),
            GetAgentSystemPrompt: jest.fn().mockResolvedValue(''),
            SetAgentSystemPrompt: jest.fn(),
            AddAgent: jest.fn(),
            UpdateAgent: jest.fn(),
            RemoveAgent: jest.fn(),
            SyncAgentUsers: jest.fn().mockRejectedValue('board app is not ready'),
        }
        anyWindow.go = {main: {App: bindings}}

        render(wrapIntl(
            <AgentsDialog
                board={board}
                onClose={jest.fn()}
            />,
        ))

        await waitFor(() => expect(screen.getByText('codex-a')).toBeInTheDocument())
        await waitFor(() => expect(screen.getByText('board app is not ready')).toBeInTheDocument())
    })

    test('creates an Agent select field and adds missing options', async () => {
        const bindings = {
            ListAgents: jest.fn().mockResolvedValue(JSON.stringify([
                {name: 'claude', kind: 'claude'},
                {name: 'codex-a', kind: 'codex'},
            ])),
            GetAgentSystemPrompt: jest.fn().mockResolvedValue(''),
            SetAgentSystemPrompt: jest.fn(),
            AddAgent: jest.fn(),
            UpdateAgent: jest.fn(),
            RemoveAgent: jest.fn(),
        }
        anyWindow.go = {main: {App: bindings}}
        mockedMutator.updateBoardCardProperties.mockResolvedValue()

        render(wrapIntl(
            <AgentsDialog
                board={board}
                onClose={jest.fn()}
            />,
        ))
        await waitFor(() => expect(screen.getByText('codex-a')).toBeInTheDocument())

        userEvent.click(screen.getByRole('button', {name: 'Sync to board'}))
        await waitFor(() => expect(mockedMutator.updateBoardCardProperties).toHaveBeenCalledTimes(1))

        const newProps = mockedMutator.updateBoardCardProperties.mock.calls[0][2]
        const agentProp = newProps.find((p) => p.name === 'Agent')!
        expect(agentProp).toBeDefined()
        expect(agentProp.type).toBe('select')
        expect(agentProp.options.map((o) => o.value)).toEqual(['claude', 'codex-a'])
    })

    test('wires an MCP server of the agent\'s own and reloads it for editing', async () => {
        const bindings = {
            ListAgents: jest.fn().mockResolvedValue(JSON.stringify([])),
            ListProxies: jest.fn().mockResolvedValue('[]'),
            GetAgentSystemPrompt: jest.fn().mockResolvedValue(''),
            SetAgentSystemPrompt: jest.fn(),
            AddAgent: jest.fn().mockResolvedValue(JSON.stringify({name: 'jojo', kind: 'junie'})),
            UpdateAgent: jest.fn(),
            RemoveAgent: jest.fn(),
        }
        anyWindow.go = {main: {App: bindings}}

        render(wrapIntl(
            <AgentsDialog
                board={board}
                onClose={jest.fn()}
            />,
        ))
        await waitFor(() => expect(screen.getByRole('button', {name: 'Add agent…'})).toBeInTheDocument())
        userEvent.click(screen.getByRole('button', {name: 'Add agent…'}))
        await waitFor(() => expect(screen.getByPlaceholderText('Name (matches the "Agent" option)')).toBeInTheDocument())

        userEvent.type(screen.getByPlaceholderText('Name (matches the "Agent" option)'), 'jojo')

        // Pasted the way a server's own README gives it, wrapper and all.
        const field = screen.getByRole('textbox', {name: /MCP servers/})
        fireEvent.change(field, {target: {value: '{"mcpServers": {"playwright": {"command": "npx", "args": ["-y", "@playwright/mcp@latest"]}}}'}})

        userEvent.click(screen.getByRole('button', {name: 'Save'}))
        await waitFor(() => expect(bindings.AddAgent).toHaveBeenCalled())
        expect(JSON.parse(bindings.AddAgent.mock.calls[0][0])).toMatchObject({
            name: 'jojo',
            mcpServers: {playwright: {command: 'npx', args: ['-y', '@playwright/mcp@latest']}},
        })
    })

    test('takes MCP servers listed instead of keyed by name', () => {
        // Some clients write them as a list; the config file reads both shapes,
        // so pasting from one of them must not be refused here either.
        expect(textToServers('[{"name": "pw", "command": "npx", "args": ["-y", "@playwright/mcp@latest"]}]')).toEqual({
            pw: {command: 'npx', args: ['-y', '@playwright/mcp@latest']},
        })
        expect(() => textToServers('[{"command": "npx"}]')).toThrow(/name/)
    })

    test('refuses to save MCP servers that are not JSON', async () => {
        const bindings = {
            ListAgents: jest.fn().mockResolvedValue(JSON.stringify([])),
            ListProxies: jest.fn().mockResolvedValue('[]'),
            GetAgentSystemPrompt: jest.fn().mockResolvedValue(''),
            SetAgentSystemPrompt: jest.fn(),
            AddAgent: jest.fn(),
            UpdateAgent: jest.fn(),
            RemoveAgent: jest.fn(),
        }
        anyWindow.go = {main: {App: bindings}}

        render(wrapIntl(
            <AgentsDialog
                board={board}
                onClose={jest.fn()}
            />,
        ))
        await waitFor(() => expect(screen.getByRole('button', {name: 'Add agent…'})).toBeInTheDocument())
        userEvent.click(screen.getByRole('button', {name: 'Add agent…'}))
        await waitFor(() => expect(screen.getByPlaceholderText('Name (matches the "Agent" option)')).toBeInTheDocument())

        userEvent.type(screen.getByPlaceholderText('Name (matches the "Agent" option)'), 'jojo')
        fireEvent.change(screen.getByRole('textbox', {name: /MCP servers/}), {target: {value: 'playwright = npx'}})
        userEvent.click(screen.getByRole('button', {name: 'Save'}))

        // Saving half a configuration is worse than not saving it.
        await waitFor(() => expect(screen.getByText(/must be valid JSON/)).toBeInTheDocument())
        expect(bindings.AddAgent).not.toHaveBeenCalled()
    })

    test('round-trips the MCP server list through the form', async () => {
        const bindings = {
            ListAgents: jest.fn().mockResolvedValue(JSON.stringify([
                {name: 'jojo', kind: 'junie', mcpServers: {playwright: {command: 'npx', args: ['-y', '@playwright/mcp@latest']}}},
            ])),
            ListProxies: jest.fn().mockResolvedValue('[]'),
            GetAgentSystemPrompt: jest.fn().mockResolvedValue(''),
            SetAgentSystemPrompt: jest.fn(),
            AddAgent: jest.fn(),
            UpdateAgent: jest.fn().mockResolvedValue('{}'),
            RemoveAgent: jest.fn(),
        }
        anyWindow.go = {main: {App: bindings}}

        render(wrapIntl(
            <AgentsDialog
                board={board}
                onClose={jest.fn()}
            />,
        ))
        await waitFor(() => expect(screen.getByText('jojo')).toBeInTheDocument())
        userEvent.click(screen.getAllByRole('button', {name: 'Edit'})[0])

        // Reloaded as the same JSON, wrapper included, so it can be edited and
        // pasted onwards.
        await waitFor(() => expect(screen.getByRole('textbox', {name: /MCP servers/})).toHaveValue(
            JSON.stringify({mcpServers: {playwright: {command: 'npx', args: ['-y', '@playwright/mcp@latest']}}}, null, 2),
        ))
        userEvent.click(screen.getByRole('button', {name: 'Save'}))
        await waitFor(() => expect(bindings.UpdateAgent).toHaveBeenCalled())
        expect(JSON.parse(bindings.UpdateAgent.mock.calls[0][0])).toMatchObject({
            mcpServers: {playwright: {command: 'npx', args: ['-y', '@playwright/mcp@latest']}},
        })
    })
})
