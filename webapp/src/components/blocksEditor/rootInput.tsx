// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.
import React, {useMemo, useState} from 'react'

import Combobox from '../../widgets/combobox'
import type {ComboboxOption} from '../../combobox'

import * as registry from './blocks/'
import {ContentType} from './blocks/types'

import './rootInput.scss'

type Props = {
    onChange: (value: string) => void
    onChangeType: (blockType: ContentType) => void
    onSave: (value: string, blockType: string) => void
    value: string
}

export default function RootInput(props: Props) {
    const [showMenu, setShowMenu] = useState(false)

    const options: Array<ComboboxOption<ContentType>> = useMemo(() => registry.list().map((ct) => ({
        id: ct.slashCommand,
        label: `${ct.slashCommand} Creates a new ${ct.displayName} block.`,
        data: ct,
    })), [])

    return (
        <Combobox
            className='RootInput'
            classNamePrefix='RootInput'
            placeholder={'Introduce your text or your slash command'}
            autoFocus={true}
            menuIsOpen={showMenu}
            portalTarget={document.getElementById('focalboard-root-portal')}
            options={options}

            // A slash command matches from either end, so `/i` finds `/image`
            // and typing past the command keeps it on screen.
            matches={(option, query) => query.startsWith(option.id) || option.id.startsWith(query)}
            inputValue={props.value}
            onInputChange={(value: string) => {
                props.onChange(value)
                setShowMenu(value.startsWith('/'))
            }}
            onChange={(value) => {
                const chosen = value as ComboboxOption<ContentType> | null
                if (chosen) {
                    const args = props.value.split(' ').slice(1)
                    chosen.data.runSlashCommand(props.onChangeType, props.onChange, ...args)
                }
            }}
            onBlur={() => {
                const command = props.value.trimStart().split(' ')[0]
                const block = registry.getBySlashCommandPrefix(command)
                if (command === '' || !block) {
                    props.onSave(props.value, 'text')
                    props.onChange('')
                }
            }}
            onFocus={(e: React.FocusEvent) => {
                e.currentTarget.scrollIntoView({block: 'center'})
            }}
            onKeyDown={(e) => {
                if (e.key === 'Escape') {
                    props.onSave('', 'text')
                    props.onChange('')
                }
                if (e.key === 'Enter') {
                    const command = props.value.trimStart().split(' ')[0]
                    const block = registry.getBySlashCommandPrefix(command)
                    if (command === '' || !block) {
                        e.preventDefault()
                        e.stopPropagation()
                        props.onSave(props.value, 'text')
                        props.onChange('')
                    }
                }
            }}
        />
    )
}
