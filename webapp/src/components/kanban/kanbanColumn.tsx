// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.
import React from 'react'

import {useDropZone} from '../../hooks/sortable'

import {Card} from '../../blocks/card'
import './kanbanColumn.scss'

type Props = {
    onDrop: (card: Card) => void
    children: React.ReactNode
}

const KanbanColumn = (props: Props) => {
    const [isOver, drop] = useDropZone<Card>('card', true, props.onDrop)

    let className = 'octo-board-column'
    if (isOver) {
        className += ' dragover'
    }
    return (
        <div
            ref={drop}
            className={className}
        >
            {props.children}
        </div>
    )
}

export default React.memo(KanbanColumn)
