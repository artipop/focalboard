// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.
import React from 'react'
import {render, screen, waitFor} from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import '@testing-library/jest-dom'

import {IPropertyOption, IPropertyTemplate} from '../../blocks/board'
import {wrapIntl} from '../../testUtils'

import ColumnSettingsDialog, {isColumnSettingsAvailable, specFor} from './columnSettingsDialog'
import ColumnBadge, {invalidateBoardColumns} from './columnBadge'

const anyWindow = window as any

const property: IPropertyTemplate = {
    id: 'prop-status',
    name: 'Status',
    type: 'select',
    options: [{id: 'opt-work', value: 'In Progress', color: 'propColorDefault'}],
}
const option: IPropertyOption = property.options[0]

const savedSpec = {
    boardId: 'board1',
    propertyId: 'prop-status',
    optionId: 'opt-work',
    property: 'Status',
    column: 'In Progress',
    action: 'agent',
    agents: ['dev-1'],
    maxRunning: 2,
}

function stubBindings(overrides: Record<string, unknown> = {}) {
    const bindings = {
        ListBoardColumns: jest.fn().mockResolvedValue(JSON.stringify([savedSpec])),
        SaveBoardColumn: jest.fn().mockResolvedValue('{}'),
        RemoveBoardColumn: jest.fn().mockResolvedValue(undefined),
        ListAgents: jest.fn().mockResolvedValue(JSON.stringify([{name: 'dev-1'}, {name: 'dev-2'}])),
        ListDeployTargets: jest.fn().mockResolvedValue('[]'),
        GetWorktreeMode: jest.fn().mockResolvedValue('always'),
        ...overrides,
    }
    anyWindow.go = {main: {App: bindings}}
    return bindings
}

describe('components/acp/columnSettingsDialog', () => {
    afterEach(() => {
        delete anyWindow.go
        invalidateBoardColumns()
        jest.clearAllMocks()
    })

    test('is unavailable without desktop bindings', () => {
        expect(isColumnSettingsAvailable()).toBe(false)
    })

    test('specFor prefers the column bound to the option over one matched by name', () => {
        const byName = {...savedSpec, optionId: undefined, action: 'test'}
        expect(specFor([byName, savedSpec], 'opt-work', 'In Progress')!.action).toBe('agent')
        expect(specFor([byName], 'opt-other', 'in progress')!.action).toBe('test')
        expect(specFor([savedSpec], 'opt-other', 'Somewhere else')).toBeUndefined()
    })

    test('shows what the column does and saves a changed crew', async () => {
        const bindings = stubBindings()
        render(wrapIntl(
            <ColumnSettingsDialog
                boardId='board1'
                property={property}
                option={option}
                onClose={jest.fn()}
            />,
        ))

        await waitFor(() => expect(screen.getByLabelText('dev-1')).toBeChecked())
        expect(screen.getByLabelText('dev-2')).not.toBeChecked()

        userEvent.click(screen.getByLabelText('dev-2'))
        userEvent.click(screen.getByRole('button', {name: 'Save'}))

        await waitFor(() => expect(bindings.SaveBoardColumn).toHaveBeenCalled())
        const saved = JSON.parse(bindings.SaveBoardColumn.mock.calls[0][0])
        expect(saved.agents).toEqual(['dev-1', 'dev-2'])

        // The option is what the column is; the name only rides along.
        expect(saved.optionId).toBe('opt-work')
        expect(saved.maxRunning).toBe(2)
    })

    test('a validation error from Go is shown, not swallowed', async () => {
        stubBindings({SaveBoardColumn: jest.fn().mockRejectedValue('агент "ghost" не найден в реестре')})
        render(wrapIntl(
            <ColumnSettingsDialog
                boardId='board1'
                property={property}
                option={option}
                onClose={jest.fn()}
            />,
        ))
        await waitFor(() => expect(screen.getByLabelText('dev-1')).toBeChecked())
        userEvent.click(screen.getByRole('button', {name: 'Save'}))

        await waitFor(() => expect(screen.getByText(/не найден в реестре/)).toBeInTheDocument())
    })
})

describe('components/acp/columnBadge', () => {
    afterEach(() => {
        delete anyWindow.go
        invalidateBoardColumns()
        jest.clearAllMocks()
    })

    test('says on the column what happens in it', async () => {
        stubBindings()
        render(wrapIntl(
            <ColumnBadge
                boardId='board1'
                optionId='opt-work'
                columnName='In Progress'
            />,
        ))

        // One agent is named outright; the limit rides next to it.
        await waitFor(() => expect(screen.getByText('dev-1')).toBeInTheDocument())
        expect(screen.getByText('2')).toBeInTheDocument()
    })

    test('a column that does nothing says nothing', async () => {
        stubBindings({ListBoardColumns: jest.fn().mockResolvedValue(JSON.stringify([{...savedSpec, action: 'none'}]))})
        const {container} = render(wrapIntl(
            <ColumnBadge
                boardId='board1'
                optionId='opt-work'
                columnName='In Progress'
            />,
        ))
        await waitFor(() => expect(container).toBeEmptyDOMElement())
    })
})
