// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.
import React, {useMemo, useState} from 'react'
import {act, render} from '@testing-library/react'
import {useDragDropManager, useDragDropMonitor} from '@dnd-kit/react'

import {setupDragEnvironment, setRect, drag, flick} from '../test/dragEnvironment'

import {SortableProvider, useSortable, useDropZone} from './sortable'

setupDragEnvironment()

type Item = {id: string}

function Card(props: {item: Item, onDrop: (src: Item, dst: Item) => void}) {
    const [, , ref] = useSortable('card', props.item, true, props.onDrop)
    return (
        <div
            ref={ref}
            data-testid={props.item.id}
        />
    )
}

function Zone(props: {onDrop: (src: Item) => void}) {
    const [, ref] = useDropZone<Item>('card', true, props.onDrop)
    return (
        <div
            ref={ref}
            data-testid='zone'
        />
    )
}

describe('hooks/sortable dragging', () => {
    const a = {id: 'a'}
    const b = {id: 'b'}

    function setup() {
        const onCard = jest.fn()
        const onZone = jest.fn()
        const {getByTestId} = render(
            <SortableProvider>
                <Card
                    item={a}
                    onDrop={onCard}
                />
                <Card
                    item={b}
                    onDrop={onCard}
                />
                <Zone onDrop={onZone}/>
            </SortableProvider>,
        )
        setRect(getByTestId('a'), {x: 0, y: 0, width: 100, height: 40})
        setRect(getByTestId('b'), {x: 0, y: 100, width: 100, height: 40})
        setRect(getByTestId('zone'), {x: 0, y: 200, width: 100, height: 100})
        return {getByTestId, onCard, onZone}
    }

    it('drops a card onto another card', async () => {
        const {getByTestId, onCard} = setup()

        await drag(act, getByTestId('a'), {x: 50, y: 120})

        expect(onCard).toHaveBeenCalledWith(a, b)
    })

    it('drops a card into a zone that holds none', async () => {
        const {getByTestId, onZone} = setup()

        await drag(act, getByTestId('a'), {x: 50, y: 250})

        expect(onZone).toHaveBeenCalledWith(a)
    })

    // Note this passes with dnd-kit's defaults too: the abort it guards against
    // hangs off a real 200ms timer that jsdom does not reproduce. It is here for
    // what it does assert -- that a single-move drag lands -- not as a guard.
    //
    // Moving the mouse quickly is not a mistake the user made. dnd-kit's default
    // pairs a Delay with a Distance under one controller, and the Delay aborts
    // the whole activation if the pointer outruns its tolerance before the timer
    // fires -- so a flick produces a beforedragstart, a canceled dragend, and no
    // drag. Whether dragging worked came down to how fast the hand moved.
    it('drops when the pointer is moved in one quick sweep', async () => {
        const {getByTestId, onZone} = setup()

        await flick(act, getByTestId('a'), {x: 50, y: 250})

        expect(onZone).toHaveBeenCalledWith(a)
    })

    // Dropping a card moves it, which unmounts the element that was being
    // dragged. dnd-kit resets a finished operation only once that element
    // reports itself idle at the end of its drop animation -- so an element that
    // leaves mid-animation leaves the manager believing a drag is still running,
    // and every press after it is ignored. Two drags in a row is the whole test.
    it('still works after the dragged card has gone away', async () => {
        const onCard = jest.fn()
        const onZone = jest.fn()

        function Board(props: {probe: React.ReactNode}) {
            const [gone, setGone] = useState(false)
            return (
                <SortableProvider>
                    {props.probe}
                    {!gone && (
                        <Card
                            item={a}
                            onDrop={(src, dst) => {
                                onCard(src, dst)
                                setGone(true)
                            }}
                        />
                    )}
                    <Card
                        item={b}
                        onDrop={onCard}
                    />
                    <Zone onDrop={onZone}/>
                </SortableProvider>
            )
        }

        let manager: {dragOperation: {status: {idle: boolean}}} | null = null

        function Probe() {
            manager = useDragDropManager() as never
            return null
        }

        const {getByTestId} = render(<Board probe={<Probe/>}/>)
        setRect(getByTestId('a'), {x: 0, y: 0, width: 100, height: 40})
        setRect(getByTestId('b'), {x: 0, y: 100, width: 100, height: 40})
        setRect(getByTestId('zone'), {x: 0, y: 200, width: 100, height: 100})

        await drag(act, getByTestId('a'), {x: 50, y: 120})
        expect(onCard).toHaveBeenCalledWith(a, b)

        // The second drag is only half of it. dnd-kit refuses a press outright
        // unless the operation is idle, and refuses it in silence, so a board
        // that never got back to idle looks like a board nobody pressed. Say
        // which of the two failed.
        expect(manager!.dragOperation.status.idle).toBe(true)

        await drag(act, getByTestId('b'), {x: 50, y: 250})
        expect(onZone).toHaveBeenCalledWith(b)
    })

    // Between beforedragstart and dragstart the manager parks the operation in
    // 'initializing' and waits on `renderer.rendering` before promoting it to
    // 'dragging'. Any on* handler passed to DragDropProvider makes that promise
    // a pending one, resolved only when a startTransition() commits -- so on a
    // board that re-renders constantly, becoming a drag queues behind
    // low-priority React work, and a pointerup meanwhile cancels the operation
    // outright (`canceled = !status.initialized`). That produced a
    // beforedragstart and a canceled dragend with no dragstart at all, which is
    // how dragging died intermittently and then for good.
    //
    // The starvation itself cannot be staged in jsdom, where act() flushes
    // everything. What can be asserted is the property whose loss allows it:
    // when the drag begins, nothing is gating it.
    it('does not make the start of a drag wait on a React render', async () => {
        let gated: boolean | undefined

        function Probe() {
            const manager = useDragDropManager()
            useDragDropMonitor(useMemo(() => ({
                onBeforeDragStart: () => {
                    // Read a turn late, on purpose. Effects run child-first, so
                    // this listener is registered -- and fires -- before the
                    // provider's own, which is what would park the promise.
                    queueMicrotask(() => {
                        let settled = false
                        manager?.renderer.rendering.then(() => {
                            settled = true
                        })

                        // Two more turns: enough for an already-resolved promise
                        // to have run its reaction, far too early for a commit.
                        queueMicrotask(() => queueMicrotask(() => {
                            gated = !settled
                        }))
                    })
                },
            }), [manager]))
            return null
        }

        const onCard = jest.fn()
        const {getByTestId} = render(
            <SortableProvider>
                <Probe/>
                <Card
                    item={a}
                    onDrop={onCard}
                />
                <Card
                    item={b}
                    onDrop={onCard}
                />
            </SortableProvider>,
        )
        setRect(getByTestId('a'), {x: 0, y: 0, width: 100, height: 40})
        setRect(getByTestId('b'), {x: 0, y: 100, width: 100, height: 40})

        await drag(act, getByTestId('a'), {x: 50, y: 120})

        expect(gated).toBe(false)
        expect(onCard).toHaveBeenCalledWith(a, b)
    })

    // The card being dragged is a drop target too, and it follows the pointer,
    // so without care it wins every collision and the drop is discarded as a
    // drop onto itself -- which is exactly how dragging stopped working.
    it('never lands on the card being dragged', async () => {
        const {getByTestId, onCard} = setup()

        await drag(act, getByTestId('a'), {x: 50, y: 20})

        expect(onCard).not.toHaveBeenCalled()
    })
})
