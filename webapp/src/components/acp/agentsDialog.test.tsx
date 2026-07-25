// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.
import React from 'react'
import {render, screen, waitFor} from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import '@testing-library/jest-dom'

import {wrapIntl} from '../../testUtils'
import {TestBlockFactory} from '../../test/testBlockFactory'
import mutator from '../../mutator'

import AgentsDialog, {isAgentsAvailable} from './agentsDialog'

jest.mock('../../mutator')
const mockedMutator = jest.mocked(mutator, true)

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
        await waitFor(() => expect(screen.getByRole('combobox')).toBeInTheDocument())

        userEvent.selectOptions(screen.getByRole('combobox'), 'codex')
        userEvent.type(screen.getByPlaceholderText('Name (matches the "Agent" option)'), 'codex-a')
        userEvent.type(screen.getByPlaceholderText('CODEX_HOME=/Users/me/.codex-work'), 'CODEX_HOME=/tmp/x')

        userEvent.click(screen.getByRole('button', {name: 'Save'}))
        await waitFor(() => expect(bindings.AddAgent).toBeCalled())
        const payload = JSON.parse(bindings.AddAgent.mock.calls[0][0])
        expect(payload).toMatchObject({name: 'codex-a', kind: 'codex', env: {CODEX_HOME: '/tmp/x'}})
    })

    test('saves a wrapped launch command and per-agent proxy settings', async () => {
        const bindings = {
            ListAgents: jest.fn().mockResolvedValue(JSON.stringify([])),
            GetAgentSystemPrompt: jest.fn().mockResolvedValue(''),
            SetAgentSystemPrompt: jest.fn(),
            AddAgent: jest.fn().mockResolvedValue(JSON.stringify({name: 'corp', kind: 'claude'})),
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
        await waitFor(() => expect(screen.getByRole('combobox')).toBeInTheDocument())

        userEvent.type(screen.getByPlaceholderText('Name (matches the "Agent" option)'), 'corp')

        // The launch command is offered for claude too, and quoted arguments
        // stay a single argv element.
        userEvent.type(screen.getByPlaceholderText('proxychains4 -q -f /etc/corp.conf claude'), 'proxychains4 -f "/etc/my conf.conf" claude')
        userEvent.type(screen.getByPlaceholderText('http://proxy.corp:3128'), 'http://proxy.corp:3128')
        userEvent.type(screen.getByPlaceholderText('localhost,127.0.0.1,.corp'), '.corp')
        userEvent.type(screen.getByPlaceholderText('/etc/ssl/corp-ca.pem'), '/etc/ssl/corp-ca.pem')

        userEvent.click(screen.getByRole('button', {name: 'Save'}))
        await waitFor(() => expect(bindings.AddAgent).toBeCalled())
        const payload = JSON.parse(bindings.AddAgent.mock.calls[0][0])
        expect(payload).toMatchObject({
            name: 'corp',
            kind: 'claude',
            command: ['proxychains4', '-f', '/etc/my conf.conf', 'claude'],
            proxy: 'http://proxy.corp:3128',
            noProxy: '.corp',
            caCert: '/etc/ssl/corp-ca.pem',
        })
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
        await waitFor(() => expect(mockedMutator.updateBoardCardProperties).toBeCalledTimes(1))

        const newProps = mockedMutator.updateBoardCardProperties.mock.calls[0][2]
        const agentProp = newProps.find((p) => p.name === 'Agent')!
        expect(agentProp).toBeDefined()
        expect(agentProp.type).toBe('select')
        expect(agentProp.options.map((o) => o.value)).toEqual(['claude', 'codex-a'])
    })
})
