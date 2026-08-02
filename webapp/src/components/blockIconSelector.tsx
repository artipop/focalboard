// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.
import React from 'react'

import {BlockIcons} from '../blockIcons'
import {Card} from '../blocks/card'
import mutator from '../mutator'

import IconSelector from './iconSelector'

type Props = {
    block: Card
    size?: 's' | 'm' | 'l'
    readonly?: boolean
}

const BlockIconSelector = (props: Props) => {
    const {block, size} = props

    const onSelectEmoji = (emoji: string) => {
        mutator.changeBlockIcon(block.boardId, block.id, block.fields.icon, emoji)
        document.body.click()
    }
    const onAddRandomIcon = () => mutator.changeBlockIcon(block.boardId, block.id, block.fields.icon, BlockIcons.shared.randomIcon())
    const onRemoveIcon = () => mutator.changeBlockIcon(block.boardId, block.id, block.fields.icon, '', 'remove icon')

    if (!block.fields.icon) {
        return null
    }

    let className = `octo-icon size-${size || 'm'}`
    if (props.readonly) {
        className += ' readonly'
    }
    const iconElement = <div className={className}><span>{block.fields.icon}</span></div>

    return (
        <IconSelector
            readonly={props.readonly}
            iconElement={iconElement}
            onAddRandomIcon={onAddRandomIcon}
            onSelectEmoji={onSelectEmoji}
            onRemoveIcon={onRemoveIcon}
        />
    )
}

export default React.memo(BlockIconSelector)
