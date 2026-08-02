// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.
import React, {type JSX, useId, useRef} from 'react'
import {DragDropProvider, useDraggable, useDroppable, type DragEndEvent} from '@dnd-kit/react'

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

export function SortableProvider(props: {children: React.ReactNode}): JSX.Element {
    const onDragEnd = (event: DragEndEvent) => {
        if (event.canceled) {
            return
        }
        const {source, target} = event.operation
        if (!source || !target) {
            return
        }

        // Every card is both draggable and droppable, so dropping one on itself
        // has to be a no-op. react-dnd never reported that; dnd-kit can.
        const from = (source.data as DraggableData | undefined)?.item
        const to = target.data as DroppableData | undefined

        // Sortables registered elsewhere (the sidebar) share this provider but
        // handle their own dragend, so anything without a handler is not ours.
        if (!to || typeof to.handler !== 'function' || from === undefined || from === to.item) {
            return
        }
        to.handler(from as never, to.item as never)
    }

    return (
        <DragDropProvider onDragEnd={onDragEnd}>
            {props.children}
        </DragDropProvider>
    )
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

    const {isDropTarget} = useDroppable<DroppableData>({
        id: `drop-${itemType}-${id}`,
        type: itemType,
        accept: itemType,
        element: ref,
        disabled: !enabled,
        data: {item, handler: handler as (src: never, dst: never) => void},
    })

    return {isDragging, isOver: isDropTarget, ref, handleRef}
}

// A zone that only receives. react-dnd needed `monitor.isOver({shallow: true})`
// here so an outer zone would not also claim a drop meant for a card inside it;
// dnd-kit resolves collisions to a single target, so that is now the default.
export function useDropZone<T>(itemType: string, enabled: boolean, handler: (src: T) => void): [boolean, React.RefObject<HTMLDivElement | null>] {
    const id = useId()
    const ref = useRef<HTMLDivElement>(null)

    const {isDropTarget} = useDroppable<DroppableData>({
        id: `zone-${itemType}-${id}`,
        type: itemType,
        accept: itemType,
        element: ref,
        disabled: !enabled,
        data: {item: undefined, handler: ((src: T) => handler(src)) as unknown as (src: never, dst: never) => void},
    })

    return [isDropTarget, ref]
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
