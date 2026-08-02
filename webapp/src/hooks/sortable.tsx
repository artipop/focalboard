// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.
import React, {type JSX, useId, useMemo, useRef} from 'react'
import {DragDropProvider, useDragDropMonitor, useDraggable, useDroppable, type DragDropEventHandlers, type DragEndEvent} from '@dnd-kit/react'
import {useSortable as useDndSortable} from '@dnd-kit/react/sortable'
import {SortableKeyboardPlugin} from '@dnd-kit/dom/sortable'
import {defaultCollisionDetection, type CollisionDetector} from '@dnd-kit/collision'
import {defaultPreset, Feedback, KeyboardSensor, PointerActivationConstraints, PointerSensor} from '@dnd-kit/dom'

import {IContentBlockWithCords} from '../blocks/contentBlock'
import {Block} from '../blocks/block'

interface ISortableWithGripItem {
    block: Block | Block[]
    cords: {x: number, y?: number, z?: number}
}

// react-dnd let every drop target own its drop handler; dnd-kit dispatches a
// single dragend at the provider. To keep the call sites unchanged, each
// droppable carries its item and handler here and SortableProvider dispatches.
type DroppableData = {
    item: unknown
    handler: (src: never, dst: never) => void
}

type DraggableData = {
    item: unknown
}

// The same element is both draggable and droppable, so while a card is being
// dragged its own drop zone travels with the pointer, contains it at every
// moment, and wins every collision on pointer intersection -- after which the
// drop is thrown away as a drop onto itself and nothing else was ever
// considered. Disabling the zone when the drag starts is too late: the eligible
// targets are settled by then. Declining the collision is not.
function ignoringSelf(item: unknown): CollisionDetector {
    return (input) => {
        const source = input.dragOperation.source?.data as DraggableData | undefined
        if (source && source.item === item) {
            return null
        }
        return defaultCollisionDetection(input)
    }
}

// Our handlers move the card, which unmounts the element that was just being
// dragged -- and dragend is dispatched in the middle of dnd-kit tearing the
// operation down, not after it. What follows the dispatch is:
//
//     renderer.rendering.then(() => {
//       dragOperation.status.set('dropped')       // Feedback now sets
//       const dropping = source?.status === 'dropping'   // source.status
//       if (dropping) { wait for source.status === 'idle', then reset() }
//     })
//
// so a source destroyed during the dispatch takes its Feedback effects with it
// and nothing is left to bring its status back to idle. dragOperation.reset()
// then never runs, the operation stays in 'dropped' forever, and every later
// press is dropped on the floor by handlePointerDown's `!status.idle` guard --
// silently, which is why a dead board still logs a pointerdown and nothing else.
// Whether the unmount lands before or after that microtask is up to when React
// commits, which is why it took a few drags to wedge.
//
// A task, not a microtask: the teardown above is itself a chain of promise
// callbacks, and the point is to come after all of them.
function afterDragTeardown(run: () => void): void {
    setTimeout(run, 0)
}

// Exported so the dispatch can be tested: everything else here needs real
// pointer events and real element geometry, which jsdom has neither of.
export function dispatchDrop(event: DragEndEvent): void {
    if (event.canceled) {
        return
    }
    const {source, target} = event.operation
    if (!source || !target) {
        return
    }

    const from = (source.data as DraggableData | undefined)?.item
    const to = target.data as DroppableData | undefined

    // Sortables registered elsewhere (the sidebar) share this provider but
    // handle their own dragend, so anything without a handler is not ours.
    if (!to || typeof to.handler !== 'function' || from === undefined) {
        return
    }

    // Every card is both draggable and droppable, so dropping one on itself
    // has to be a no-op. react-dnd never reported that; dnd-kit can.
    if (from === to.item) {
        return
    }

    afterDragTeardown(() => to.handler(from as never, to.item as never))
}

// dnd-kit resets a finished operation only once the dragged element reports
// itself idle, and that happens at the end of its drop animation. Our handlers
// move the card the moment the drop lands, so React unmounts that element
// mid-animation, it never goes idle, the reset never runs -- and the manager
// goes on believing a drag is in progress, ignoring every press after it. The
// animation plays the element back to where it started, which is never where it
// belongs here; without one, dnd-kit cleans up synchronously instead.
const plugins = defaultPreset.plugins.map((plugin) => (
    plugin === Feedback ? Feedback.configure({dropAnimation: null}) : plugin
))

// dnd-kit's mouse default pairs a Delay with a Distance and gives them one
// controller, and the Delay aborts the whole activation if the pointer travels
// further than its tolerance before the timer fires:
//
//     case 'pointermove':
//       if (exceedsDistance(delta, this.options.tolerance)) this.abort()
//
// So a quick flick of the mouse -- more than 10px inside 200ms -- cancels a drag
// that the Distance constraint would have started at 5px, and the press is lost
// with a beforedragstart followed by a canceled dragend and no dragstart at all.
// Whether a drag works then depends on how fast the hand moved. Distance alone
// for a mouse, which is also what react-beautiful-dnd asked for; touch keeps a
// hold, or dragging would fight scrolling; a real grip needs no threshold,
// because holding one is already the request to drag.
const sensors = [
    PointerSensor.configure({
        activationConstraints(event, source) {
            if (event.pointerType === 'touch') {
                return [new PointerActivationConstraints.Delay({value: 250, tolerance: 5})]
            }
            if (source.handle && event.target instanceof Element && source.handle.contains(event.target)) {
                return undefined
            }
            return [new PointerActivationConstraints.Distance({value: 5})]
        },
    }),
    KeyboardSensor,
]

// These go on the manager's own monitor rather than on DragDropProvider, and
// that is not a style choice -- passing any of them as a prop stops drags from
// starting.
//
// The provider wraps onBeforeDragStart, onDragOver, onDragMove and onDragEnd in
// its renderer's trackRendering(), which parks a *pending* promise in
// renderer.rendering and only resolves it once a startTransition() commits.
// Meanwhile the manager, having just dispatched beforedragstart, does:
//
//     dragOperation.status.set('initializing')
//     this.manager.renderer.rendering.then(() => ...status.set('dragging'))
//
// So with a beforedragstart handler attached, becoming a drag waits on a
// low-priority React render of a board that re-renders on every store update.
// Until it commits the operation sits in 'initializing', and releasing the
// pointer runs `canceled = !status.initialized` -- a beforedragstart followed by
// a canceled dragend and no dragstart, which is exactly what the console showed.
// Worse, the promise is cleared only by that same commit, so a starved
// transition leaves a permanently pending promise behind and every later drag
// awaits it too: a few drags work, then none do, then one does again.
//
// useDragDropMonitor subscribes straight to manager.monitor with no
// trackRendering, so renderer.rendering stays Promise.resolve() and the
// promotion to 'dragging' happens on the next microtask.
const monitorHandlers: Partial<DragDropEventHandlers> = {
    onDragEnd: (event) => dispatchDrop(event as DragEndEvent),
}

// Module scope, so the identity never changes: useDragDropMonitor re-subscribes
// whenever the handlers object does, and a fresh object every render would tear
// the dragend listener down and back up in the middle of a drag.
function DragMonitor(): null {
    useDragDropMonitor(monitorHandlers)
    return null
}

export function SortableProvider(props: {children: React.ReactNode}): JSX.Element {
    return (
        <DragDropProvider
            plugins={plugins}
            sensors={sensors}
        >
            <DragMonitor/>
            {props.children}
        </DragDropProvider>
    )
}

// dnd-kit hands back a ref callback, and being told at mount and unmount is the
// point of it: given an element input instead, it has to re-read `.current`
// after every render to notice a swapped node, so a component that does not
// happen to re-render just then keeps a draggable bound to an element no longer
// in the document -- a card that looks normal and answers no press. Call sites
// want a RefObject all the same (tableHeader measures the column off `.current`),
// so this is both: a callback that also answers `.current`.
function useAttachRef(attach: (element: Element | null) => void): React.RefObject<HTMLDivElement | null> {
    const current = useRef<HTMLDivElement | null>(null)
    const latest = useRef(attach)
    latest.current = attach

    return useMemo(() => {
        const ref = (element: HTMLDivElement | null): void => {
            current.current = element
            latest.current(element)
        }
        Object.defineProperty(ref, 'current', {get: () => current.current})
        return ref as unknown as React.RefObject<HTMLDivElement | null>
    }, [])
}

// Where the item sits: which list, and where in it. A sortable that is told this
// is a different thing from a draggable that happens to sit next to others --
// dnd-kit can then say a card left column A for column B, rather than only that
// something was released over something else.
export type ListPosition = {
    id: string
    index: number
    group?: string
}

// An item with a place in a list, expressed as dnd-kit means it to be: one
// sortable, which owns its own draggable and droppable, rather than a draggable
// and a droppable we pair up on the same element ourselves. That pairing is what
// made a card collide with itself and needed a collision detector to decline it;
// a sortable has no such problem, and brings the live reordering that
// react-beautiful-dnd had and the hand-rolled version lost.
export function useListSortable<T>(
    itemType: string,
    item: T,
    enabled: boolean,
    handler: (src: T, dst: T) => void,
    position: ListPosition,
): [boolean, boolean, React.RefObject<HTMLDivElement | null>] {
    const {ref, isDragging, isDropTarget} = useDndSortable<DroppableData>({
        id: position.id,
        index: position.index,
        group: position.group,
        type: itemType,
        accept: itemType,
        disabled: !enabled,
        data: {item, handler: handler as (src: never, dst: never) => void},

        // Without this the default set brings OptimisticSortingPlugin, which
        // reorders the cards in the DOM itself as you drag. That is a second
        // owner of nodes React owns: it moves a card into another column, React
        // still believes the card is where it last put it, and the next render
        // that deletes the card calls removeChild on a parent that no longer
        // holds it. React throws inside commitDeletionEffectsOnFiber, the render
        // tree is broken from then on, and the board stops answering presses
        // altogether -- which is how a handful of good drags turned into a dead
        // board, silently and for good.
        //
        // Registration is global rather than per sortable, so every useSortable
        // in the application has to leave it out, not just this one.
        plugins: [SortableKeyboardPlugin],
    })

    return [isDragging, isDropTarget, useAttachRef(ref)]
}

function useSortableBase<T>(itemType: string, item: T, enabled: boolean, handler: (src: T, dst: T) => void) {
    const id = useId()

    // dnd-kit takes the element instead of handing back a ref, which is what
    // lets these hooks keep returning a RefObject: tableHeader reads .current
    // off it to measure the column, and every call site attaches it directly.
    const ref = useRef<HTMLDivElement>(null)
    const handleRef = useRef<HTMLDivElement>(null)

    const {isDragging} = useDraggable<DraggableData>({
        id: `drag-${itemType}-${id}`,
        type: itemType,
        element: ref,
        handle: handleRef,
        disabled: !enabled,
        data: {item},
    })

    // Kept stable on purpose. This board re-renders on every store update, and a
    // detector that changed identity underneath a live drag would be handed to
    // dnd-kit again mid-operation. One of the few places where an identity is
    // part of a contract rather than a performance tweak.
    const collisionDetector = useMemo(() => ignoringSelf(item), [item])

    const {isDropTarget} = useDroppable<DroppableData>({
        id: `drop-${itemType}-${id}`,
        type: itemType,
        accept: itemType,
        element: ref,
        disabled: !enabled,
        collisionDetector,
        data: {item, handler: handler as (src: never, dst: never) => void},
    })

    return {isDragging, isOver: isDropTarget, ref, handleRef}
}

// A zone that only receives. react-dnd needed `monitor.isOver({shallow: true})`
// here so an outer zone would not also claim a drop meant for a card inside it;
// dnd-kit resolves collisions to a single target, so that is now the default.
export function useDropZone<T>(itemType: string, enabled: boolean, handler: (src: T) => void): [boolean, React.RefObject<HTMLDivElement | null>] {
    const id = useId()

    const {isDropTarget, ref} = useDroppable<DroppableData>({
        id: `zone-${itemType}-${id}`,
        type: itemType,
        accept: itemType,
        disabled: !enabled,
        data: {item: undefined, handler: ((src: T) => handler(src)) as unknown as (src: never, dst: never) => void},
    })

    return [isDropTarget, useAttachRef(ref)]
}

export function useSortable<T>(itemType: string, item: T, enabled: boolean, handler: (src: T, dst: T) => void): [boolean, boolean, React.RefObject<HTMLDivElement | null>] {
    const {isDragging, isOver, ref} = useSortableBase(itemType, item, enabled, handler)
    return [isDragging, isOver, ref]
}

export function useSortableWithGrip(itemType: string, item: ISortableWithGripItem, enabled: boolean, handler: (src: IContentBlockWithCords, dst: IContentBlockWithCords) => void): [boolean, boolean, React.RefObject<HTMLDivElement | null>, React.RefObject<HTMLDivElement | null>] {
    const {isDragging, isOver, ref, handleRef} = useSortableBase(itemType, item as IContentBlockWithCords, enabled, handler)

    // The grip is the drag handle and the wrapper is both the dragged element
    // and the drop target -- the same split react-dnd wrote as drag(ref) and
    // drop(preview(previewRef)).
    return [isDragging, isOver, handleRef, ref]
}
