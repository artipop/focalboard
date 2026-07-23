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
const mockedMutator = jest.mocked(mutator, true)

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
        await waitFor(() => expect(bindings.PickDirectory).toBeCalled())
        await waitFor(() => expect(screen.getByDisplayValue('beta')).toBeInTheDocument())

        userEvent.click(screen.getByRole('button', {name: 'Add'}))
        await waitFor(() => expect(bindings.AddAgentRepo).toBeCalledWith('beta', '/tmp/beta'))
    })

    test('adds only missing repo names as property options', async () => {
        const bindings = {
            ListAgentRepos: jest.fn().mockResolvedValue(JSON.stringify([
                {name: 'alpha', path: '/tmp/alpha'},
                {name: 'value 1', path: '/tmp/existing'}, // already an option of Property 1
            ])),
            PickDirectory: jest.fn(),
            AddAgentRepo: jest.fn(),
            RemoveAgentRepo: jest.fn(),
        }
        anyWindow.go = {main: {App: bindings}}
        mockedMutator.insertPropertyOption.mockResolvedValue()

        render(wrapIntl(
            <AgentReposDialog
                board={board}
                onClose={jest.fn()}
            />,
        ))
        await waitFor(() => expect(screen.getByText('alpha')).toBeInTheDocument())

        userEvent.selectOptions(screen.getByRole('combobox'), 'property1')
        userEvent.click(screen.getByRole('button', {name: 'Add options'}))
        await waitFor(() => expect(mockedMutator.insertPropertyOption).toBeCalledTimes(1))
        expect(mockedMutator.insertPropertyOption.mock.calls[0][3].value).toBe('alpha')
    })
})
