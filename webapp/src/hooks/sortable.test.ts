// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.
import type {DragEndEvent} from '@dnd-kit/react'

import {dispatchDrop} from './sortable'

// Only the dispatch is testable here. Everything around it -- which droppable a
// pointer is over, whether a press became a drag -- needs real pointer events
// and real element geometry, and jsdom has neither. Those live in the browser
// tests marked TODO(react-19).
function dragEnd(options: {
    from?: unknown
    to?: unknown
    handler?: unknown
    canceled?: boolean
    noSource?: boolean
    noTarget?: boolean
}): DragEndEvent {
    const source = options.noSource ? null : {data: {item: options.from}}
    const target = options.noTarget ? null : {data: {item: options.to, handler: options.handler}}
    return {
        canceled: Boolean(options.canceled),
        operation: {source, target},
    } as unknown as DragEndEvent
}

// The drop is handed over a turn late on purpose -- dragend arrives in the
// middle of dnd-kit tearing the operation down, and moving the card there
// unmounts the element it is still tearing down. So every assertion here waits
// one turn, including the ones that expect nothing to happen.
const settle = () => new Promise((resolve) => setTimeout(resolve, 0))

describe('hooks/sortable dispatchDrop', () => {
    const card = {id: 'card-1'}
    const other = {id: 'card-2'}

    it('hands the dragged item to the handler of what it was dropped on', async () => {
        const handler = jest.fn()
        dispatchDrop(dragEnd({from: card, to: other, handler}))
        await settle()
        expect(handler).toHaveBeenCalledWith(card, other)
    })

    // Not merely late: the element being dragged must still be mounted while
    // dnd-kit finishes with it, or the operation never returns to idle.
    it('does not move the card while dragend is still being dispatched', async () => {
        const handler = jest.fn()
        dispatchDrop(dragEnd({from: card, to: other, handler}))
        expect(handler).not.toHaveBeenCalled()
        await settle()
        expect(handler).toHaveBeenCalledWith(card, other)
    })

    it('does nothing when the drag was canceled', async () => {
        const handler = jest.fn()
        dispatchDrop(dragEnd({from: card, to: other, handler, canceled: true}))
        await settle()
        expect(handler).not.toHaveBeenCalled()
    })

    it('does nothing when the drop landed outside any target', async () => {
        const handler = jest.fn()
        dispatchDrop(dragEnd({from: card, handler, noTarget: true}))
        dispatchDrop(dragEnd({to: other, handler, noSource: true}))
        await settle()
        expect(handler).not.toHaveBeenCalled()
    })

    // A card is its own droppable, so this is the common case, not an edge one.
    it('does nothing when an item is dropped on itself', async () => {
        const handler = jest.fn()
        dispatchDrop(dragEnd({from: card, to: card, handler}))
        await settle()
        expect(handler).not.toHaveBeenCalled()
    })

    // The sidebar's sortables share this provider and answer their own dragend.
    it('leaves targets that carry no handler alone', async () => {
        expect(() => dispatchDrop(dragEnd({from: card, to: other}))).not.toThrow()
        await settle()
    })

    // A drop zone -- a kanban column -- has no item of its own, and passing
    // undefined through as the destination is what its handler expects.
    it('dispatches to a zone that has no item of its own', async () => {
        const handler = jest.fn()
        dispatchDrop(dragEnd({from: card, to: undefined, handler}))
        await settle()
        expect(handler).toHaveBeenCalledWith(card, undefined)
    })
})
