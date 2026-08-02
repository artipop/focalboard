// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.
import React, {useId, useRef} from 'react'
import {useDraggable, useDragOperation, useDroppable} from '@dnd-kit/react'

import GripIcon from '../../widgets/icons/grip'

import AddIcon from '../../widgets/icons/add'

import Editor from './editor'
import * as registry from './blocks'
import {BlockData} from './blocks/types'

import './blockContent.scss'

type Props = {
    boardId?: string
    block: BlockData
    contentOrder: string[]
    editing: BlockData|null
    setEditing: (block: BlockData|null) => void
    setAfterBlock: (block: BlockData|null) => void
    onSave: (block: BlockData) => Promise<BlockData|null>
    onMove: (block: BlockData, beforeBlock: BlockData|null, afterBlock: BlockData|null) => Promise<void>
}

function BlockContent(props: Props) {
    const {block, editing, setEditing, onSave, contentOrder, boardId} = props

    // Kept as raw draggable/droppable rather than useSortable because this block
    // needs to know *which* block is being dragged while hovering, to say whether
    // the drop would land above or below it. react-dnd read that off the monitor;
    // dnd-kit exposes it through the live drag operation.
    const id = useId()
    const ref = useRef<HTMLDivElement>(null)
    const gripRef = useRef<HTMLSpanElement>(null)

    const {isDragging} = useDraggable({
        id: `block-drag-${id}`,
        type: 'block',
        element: ref,
        handle: gripRef,
        data: {item: block},
    })

    const {isDropTarget} = useDroppable({
        id: `block-drop-${id}`,
        type: 'block',
        accept: 'block',
        element: ref,
        data: {
            item: block,
            handler: (src: BlockData) => {
                if (src.id === block.id) {
                    return
                }
                if (contentOrder.indexOf(src.id || '') > contentOrder.indexOf(block.id || '')) {
                    props.onMove(src, block, null)
                } else {
                    props.onMove(src, null, block)
                }
            },
        },
    })

    const {source} = useDragOperation()
    const draggedId = (source?.data as {item?: BlockData} | undefined)?.item?.id
    const isOver = isDropTarget && draggedId !== block.id
    const draggingUp = Boolean(draggedId) && contentOrder.indexOf(draggedId || '') > contentOrder.indexOf(block.id || '')

    if (editing && editing.id === block.id) {
        return (
            <Editor
                onSave={async (b) => {
                    const updatedBlock = await onSave(b)
                    props.setEditing(null)
                    props.setAfterBlock(updatedBlock)
                    return updatedBlock
                }}
                id={block.id}
                initialValue={block.value}
                initialContentType={block.contentType}
            />
        )
    }

    const contentType = registry.get(block.contentType)
    if (contentType && contentType.Display) {
        const DisplayContent = contentType.Display
        return (
            <div
                ref={ref}
                data-testid='block-content'
                className={`BlockContent ${isOver && draggingUp ? 'over-up' : ''}  ${isOver && !draggingUp ? 'over-down' : ''}`}
                key={block.id}
                style={{
                    opacity: isDragging ? 0.5 : 1,
                }}
                onClick={() => {
                    setEditing(block)
                }}
            >
                <span
                    className='action'
                    data-testid='add-action'
                    onClick={(e) => {
                        e.preventDefault()
                        e.stopPropagation()
                        props.setAfterBlock(block)
                    }}
                >
                    <AddIcon/>
                </span>
                <span
                    className='action'
                    ref={gripRef}
                >
                    <GripIcon/>
                </span>
                <div className='content'>
                    <DisplayContent
                        value={block.value}
                        onChange={() => null}
                        onCancel={() => null}
                        onSave={(value) => onSave({...block, value})}
                        currentBoardId={boardId}
                    />
                </div>
            </div>
        )
    }
    return null
}

export default BlockContent
