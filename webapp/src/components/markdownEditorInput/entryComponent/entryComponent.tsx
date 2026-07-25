// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.
import React, {ReactElement} from 'react'
import {FormattedMessage} from 'react-intl'

import GuestBadge from '../../../widgets/guestBadge'

import './entryComponent.scss'

const BotBadge = (window as any).Components?.BotBadge

export type MentionUser = {
    user: {id: string} & Record<string, any>
    name: string
    avatar: string
    is_bot: boolean
    is_guest: boolean
    displayName: string
    isBoardMember: boolean
}

type Props = {
    mention: MentionUser
    isSelected: boolean
    onClick: () => void
    onMouseEnter: () => void
}

// A single row in the mentions typeahead popover. Markup mirrors the original
// draft-js-plugins Entry so the existing entryComponent.scss keeps applying.
const Entry = (props: Props): ReactElement => {
    const {mention, isSelected, onClick, onMouseEnter} = props

    return (
        <div
            className='EntryContainer'
            role='option'
            aria-selected={isSelected}
            onMouseDown={(e) => e.preventDefault()}
            onMouseEnter={onMouseEnter}
            onClick={onClick}
        >
            <div className='EntryComponent'>
                <div className='EntryComponent__left'>
                    <img
                        src={mention.avatar}
                        className='mentionSuggestionsEntryAvatar'
                        role='presentation'
                    />
                    <div className='mentionSuggestionsEntryText'>
                        {mention.name}
                        {BotBadge && mention.is_bot && <BotBadge/>}
                        <GuestBadge show={mention.is_guest}/>
                    </div>
                    <div className='mentionSuggestionsEntryText'>
                        {mention.displayName}
                    </div>
                </div>
                {!mention.isBoardMember &&
                    <div className='EntryComponent__hint mentionSuggestionsEntryText'>
                        <FormattedMessage
                            id='MentionSuggestion.is-not-board-member'
                            defaultMessage='(not board member)'
                        />
                    </div>}
            </div>
        </div>
    )
}

export default Entry
