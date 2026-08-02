// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.
import React from 'react'
import {render, screen, within} from '@testing-library/react'
import '@testing-library/jest-dom'
import {MemoryRouter} from 'react-router-dom'

import {Provider as ReduxProvider} from 'react-redux'

import userEvent from '@testing-library/user-event'

import {mocked} from 'jest-mock'

import Mutator from '../../mutator'
import {Utils} from '../../utils'

import {TestBlockFactory} from '../../test/testBlockFactory'
import {IPropertyTemplate} from '../../blocks/board'
import {mockStateStore, wrapDNDIntl} from '../../testUtils'

import KanbanCard from './kanbanCard'

jest.mock('../../mutator')
jest.mock('../../utils')
jest.mock('../../telemetry/telemetryClient')
const mockedUtils = mocked(Utils)
const mockedMutator = mocked(Mutator)

describe('src/components/kanban/kanbanCard', () => {
    const board = TestBlockFactory.createBoard()
    const card = TestBlockFactory.createCard(board)

    // Utils is mocked here, so createGuid returns undefined and the card would
    // have no id -- which dnd-kit reads as `source?.id === this.id`, i.e. every
    // card claiming to be the one being dragged. Give it one.
    card.id = 'card_id_1'
    const propertyTemplate: IPropertyTemplate = {
        id: 'id',
        name: 'name',
        type: 'text',
        options: [
            {
                color: 'propColorOrange',
                id: 'property_value_id_1',
                value: 'Q1',
            },
            {
                color: 'propColorBlue',
                id: 'property_value_id_2',
                value: 'Q2',
            },
        ],
    }
    const state = {
        cards: {
            cards: [card],
        },
        teams: {
            current: {id: 'team-id'},
        },
        boards: {
            current: 'board_id_1',
            boards: {
                board_id_1: {id: 'board_id_1'},
            },
            myBoardMemberships: {
                board_id_1: {userId: 'user_id_1', schemeAdmin: true},
            },
        },
        contents: {},
        comments: {
            comments: {},
        },
        users: {
            me: {
                id: 'user_id_1',
                props: {},
            },
        },
    }
    const store = mockStateStore([], state)
    beforeEach(jest.clearAllMocks)

    // The native HTML5 attribute, left over from react-dnd's HTML5 backend.
    // dnd-kit drags on pointer events, and when it finds a natively draggable
    // element it stands aside for the browser -- it binds `dragstart` to its own
    // cancel. So the browser began a native drag nobody handles, sent
    // pointercancel, and dnd-kit called off a drag that had never started: a
    // pointerdown with nothing after it, not even a pointerup. Whether the
    // native drag or the five-pixel threshold got there first came down to how
    // fast the hand moved, which is what made it look random.
    test('does not offer the card to the browser own drag and drop', () => {
        const {container} = render(wrapDNDIntl(
            <ReduxProvider store={store}>
                <KanbanCard
                    card={card}
                    board={board}
                    visiblePropertyTemplates={[propertyTemplate]}
                    visibleBadges={false}
                    isSelected={false}
                    readonly={false}
                    onDrop={jest.fn()}
                    showCard={jest.fn()}
                    isManualSort={false}
                    index={0}
                    groupId='group-1'
                />
            </ReduxProvider>,
        ), {wrapper: MemoryRouter})

        expect(container.querySelector('[draggable="true"]')).toBeNull()
    })

    test('should match snapshot', () => {
        const {container} = render(wrapDNDIntl(
            <ReduxProvider store={store}>
                <KanbanCard
                    card={card}
                    board={board}
                    visiblePropertyTemplates={[propertyTemplate]}
                    visibleBadges={false}
                    isSelected={false}
                    readonly={false}
                    onDrop={jest.fn()}
                    showCard={jest.fn()}
                    isManualSort={false}
                    index={0}
                    groupId='group-1'
                />
            </ReduxProvider>,
        ), {wrapper: MemoryRouter})
        expect(container).toMatchSnapshot()
    })
    test('should match snapshot with readonly', () => {
        const {container} = render(wrapDNDIntl(
            <ReduxProvider store={store}>
                <KanbanCard
                    card={card}
                    board={board}
                    visiblePropertyTemplates={[propertyTemplate]}
                    visibleBadges={false}
                    isSelected={false}
                    readonly={true}
                    onDrop={jest.fn()}
                    showCard={jest.fn()}
                    isManualSort={false}
                    index={0}
                    groupId='group-1'
                />
            </ReduxProvider>,
        ), {wrapper: MemoryRouter})
        expect(container).toMatchSnapshot()
    })
    test('return kanbanCard and click on delete menu ', () => {
        const result = render(wrapDNDIntl(
            <ReduxProvider store={store}>
                <KanbanCard
                    card={card}
                    board={board}
                    visiblePropertyTemplates={[propertyTemplate]}
                    visibleBadges={false}
                    isSelected={false}
                    readonly={false}
                    onDrop={jest.fn()}
                    showCard={jest.fn()}
                    isManualSort={false}
                    index={0}
                    groupId='group-1'
                />
            </ReduxProvider>,
        ), {wrapper: MemoryRouter})

        const {container} = result

        const elementMenuWrapper = screen.getByRole('button', {name: 'menuwrapper'})
        expect(elementMenuWrapper).not.toBeNull()
        userEvent.click(elementMenuWrapper)
        expect(container).toMatchSnapshot()
        const elementButtonDelete = within(elementMenuWrapper).getByRole('button', {name: 'Delete'})
        expect(elementButtonDelete).not.toBeNull()
        userEvent.click(elementButtonDelete)

        const confirmDialog = screen.getByTitle('Confirmation Dialog Box')
        expect(confirmDialog).toBeDefined()
        const confirmButton = within(confirmDialog).getByRole('button', {name: 'Delete'})
        expect(confirmButton).toBeDefined()
        userEvent.click(confirmButton)

        expect(mockedMutator.deleteBlock).toHaveBeenCalledWith(card, 'delete card')
    })

    test('return kanbanCard and click on duplicate menu ', () => {
        const {container} = render(wrapDNDIntl(
            <ReduxProvider store={store}>
                <KanbanCard
                    card={card}
                    board={board}
                    visiblePropertyTemplates={[propertyTemplate]}
                    visibleBadges={false}
                    isSelected={false}
                    readonly={false}
                    onDrop={jest.fn()}
                    showCard={jest.fn()}
                    isManualSort={false}
                    index={0}
                    groupId='group-1'
                />
            </ReduxProvider>,
        ), {wrapper: MemoryRouter})
        const elementMenuWrapper = screen.getByRole('button', {name: 'menuwrapper'})
        expect(elementMenuWrapper).not.toBeNull()
        userEvent.click(elementMenuWrapper)
        expect(container).toMatchSnapshot()
        const elementButtonDuplicate = within(elementMenuWrapper).getByRole('button', {name: 'Duplicate'})
        expect(elementButtonDuplicate).not.toBeNull()
        userEvent.click(elementButtonDuplicate)
        expect(mockedMutator.duplicateCard).toHaveBeenCalledTimes(1)
    })

    test('return kanbanCard and click on copy link menu ', () => {
        const {container} = render(wrapDNDIntl(
            <ReduxProvider store={store}>
                <KanbanCard
                    card={card}
                    board={board}
                    visiblePropertyTemplates={[propertyTemplate]}
                    visibleBadges={false}
                    isSelected={false}
                    readonly={false}
                    onDrop={jest.fn()}
                    showCard={jest.fn()}
                    isManualSort={false}
                    index={0}
                    groupId='group-1'
                />
            </ReduxProvider>,
        ), {wrapper: MemoryRouter})
        const elementMenuWrapper = screen.getByRole('button', {name: 'menuwrapper'})
        expect(elementMenuWrapper).not.toBeNull()
        userEvent.click(elementMenuWrapper)
        expect(container).toMatchSnapshot()
        const elementButtonCopyLink = within(elementMenuWrapper).getByRole('button', {name: 'Copy link'})
        expect(elementButtonCopyLink).not.toBeNull()
        userEvent.click(elementButtonCopyLink)
        expect(mockedUtils.copyTextToClipboard).toHaveBeenCalledTimes(1)
    })
})
