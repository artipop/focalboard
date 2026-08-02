// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.
import React from 'react'
import {render, screen, waitFor} from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import '@testing-library/jest-dom'

import {wrapIntl} from '../../testUtils'
import {TestBlockFactory} from '../../test/testBlockFactory'
import mutator from '../../mutator'

import AgentReposDialog, {isAgentReposAvailable} from './agentReposDialog'

jest.mock('../../mutator')
const mockedMutator = jest.mocked(mutator)

const anyWindow = window as any

describe('components/acp/agentReposDialog', () => {
    const board = TestBlockFactory.createBoard()

    afterEach(() => {
        delete anyWindow.go
        jest.clearAllMocks()
    })

    test('isAgentReposAvailable is false without desktop bindings', () => {
        expect(isAgentReposAvailable()).toBe(false)
    })

    test('lists repos and adds a picked directory', async () => {
        const bindings = {
            ListAgentRepos: jest.fn().mockResolvedValue(JSON.stringify([{name: 'alpha', path: '/tmp/alpha'}])),
            PickDirectory: jest.fn().mockResolvedValue('/tmp/beta'),
            AddAgentRepo: jest.fn().mockResolvedValue(JSON.stringify({name: 'beta', path: '/tmp/beta'})),
            RemoveAgentRepo: jest.fn().mockResolvedValue(undefined),
        }
        anyWindow.go = {main: {App: bindings}}
        expect(isAgentReposAvailable()).toBe(true)

        render(wrapIntl(
            <AgentReposDialog
                board={board}
                onClose={jest.fn()}
            />,
        ))
        await waitFor(() => expect(screen.getByText('alpha')).toBeInTheDocument())

        userEvent.click(screen.getByRole('button', {name: 'Add repository…'}))
        await waitFor(() => expect(bindings.PickDirectory).toHaveBeenCalled())
        await waitFor(() => expect(screen.getByDisplayValue('beta')).toBeInTheDocument())

        userEvent.click(screen.getByRole('button', {name: 'Add'}))
        await waitFor(() => expect(bindings.AddAgentRepo).toHaveBeenCalledWith('beta', '/tmp/beta'))
    })

    test('creates a Repositories field and adds missing repo options', async () => {
        const bindings = {
            ListAgentRepos: jest.fn().mockResolvedValue(JSON.stringify([
                {name: 'alpha', path: '/tmp/alpha'},
                {name: 'beta', path: '/tmp/beta'},
            ])),
            PickDirectory: jest.fn(),
            AddAgentRepo: jest.fn(),
            RemoveAgentRepo: jest.fn(),
        }
        anyWindow.go = {main: {App: bindings}}
        mockedMutator.updateBoardCardProperties.mockResolvedValue()

        render(wrapIntl(
            <AgentReposDialog
                board={board}
                onClose={jest.fn()}
            />,
        ))
        await waitFor(() => expect(screen.getByText('alpha')).toBeInTheDocument())

        // No button to press: opening the dialog is what puts the registry into
        // the board's field.
        await waitFor(() => expect(mockedMutator.updateBoardCardProperties).toHaveBeenCalledTimes(1))

        const newProps = mockedMutator.updateBoardCardProperties.mock.calls[0][2]
        const repoProp = newProps.find((p) => p.name === 'Repositories')!
        expect(repoProp).toBeDefined()
        expect(repoProp.type).toBe('multiSelect')
        expect(repoProp.options.map((o) => o.value)).toEqual(['alpha', 'beta'])
    })

    test('reuses an existing Repositories field and skips existing options', async () => {
        const boardWithRepos = TestBlockFactory.createBoard()
        boardWithRepos.cardProperties.push({
            id: 'repoprop',
            name: 'Repositories',
            type: 'multiSelect',
            options: [{id: 'o1', value: 'alpha', color: 'propColorDefault'}],
        })
        const bindings = {
            ListAgentRepos: jest.fn().mockResolvedValue(JSON.stringify([
                {name: 'alpha', path: '/tmp/alpha'}, // already an option
                {name: 'beta', path: '/tmp/beta'},
            ])),
            PickDirectory: jest.fn(),
            AddAgentRepo: jest.fn(),
            RemoveAgentRepo: jest.fn(),
        }
        anyWindow.go = {main: {App: bindings}}
        mockedMutator.updateBoardCardProperties.mockResolvedValue()

        render(wrapIntl(
            <AgentReposDialog
                board={boardWithRepos}
                onClose={jest.fn()}
            />,
        ))
        await waitFor(() => expect(screen.getByText('beta')).toBeInTheDocument())
        await waitFor(() => expect(mockedMutator.updateBoardCardProperties).toHaveBeenCalledTimes(1))

        const newProps = mockedMutator.updateBoardCardProperties.mock.calls[0][2]
        const repoProps = newProps.filter((p) => p.name === 'Repositories')
        expect(repoProps).toHaveLength(1) // reused, not duplicated
        expect(repoProps[0].options.map((o) => o.value)).toEqual(['alpha', 'beta'])
    })

    test('leaves the board alone when its field already lists every repository', async () => {
        const boardWithRepos = TestBlockFactory.createBoard()
        boardWithRepos.cardProperties.push({
            id: 'repoprop',
            name: 'Repositories',
            type: 'multiSelect',
            options: [{id: 'o1', value: 'alpha', color: 'propColorDefault'}],
        })
        const bindings = {
            ListAgentRepos: jest.fn().mockResolvedValue(JSON.stringify([{name: 'alpha', path: '/tmp/alpha'}])),
            PickDirectory: jest.fn(),
            AddAgentRepo: jest.fn(),
            RemoveAgentRepo: jest.fn(),
        }
        anyWindow.go = {main: {App: bindings}}

        render(wrapIntl(
            <AgentReposDialog
                board={boardWithRepos}
                onClose={jest.fn()}
            />,
        ))
        await waitFor(() => expect(screen.getByText('alpha')).toBeInTheDocument())

        // Nothing to add: the board is not patched, so the undo history and the
        // websocket stay quiet every time the dialog is opened.
        expect(mockedMutator.updateBoardCardProperties).not.toHaveBeenCalled()
    })
})
