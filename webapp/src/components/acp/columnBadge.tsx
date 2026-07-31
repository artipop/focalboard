// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

// The Wails-generated Go bindings are PascalCase methods, not constructors.
/* eslint-disable new-cap */
import React, {useCallback, useEffect, useState} from 'react'
import {useIntl} from 'react-intl'

import {agentBindings} from './agentReposDialog'
import {ColumnSpec, specFor} from './columnSettingsDialog'

import './columnBadge.scss'

// What a column does, said on the column itself. Without this the whole
// mechanism is invisible on the board: you have to open a dialog to learn that
// cards dropped here are picked up by an agent.

// A board's columns are asked for once and shared by every header on it. The
// list is small and changes only when somebody edits it, so a module-level
// cache beats a request per column.
const cache = new Map<string, ColumnSpec[]>()
const listeners = new Set<() => void>()

// invalidateBoardColumns drops the cache — after an edit, or when a session
// starts or ends and the "2/2" may have changed.
export function invalidateBoardColumns(): void {
    cache.clear()
    listeners.forEach((notify) => notify())
}

async function loadColumns(boardId: string): Promise<ColumnSpec[]> {
    const bindings = agentBindings()
    if (!bindings?.ListBoardColumns) {
        return []
    }
    const cached = cache.get(boardId)
    if (cached) {
        return cached
    }
    const specs: ColumnSpec[] = JSON.parse(await bindings.ListBoardColumns(boardId)) || []
    cache.set(boardId, specs)
    return specs
}

// useBoardColumns gives a component the board's column settings, reloading them
// whenever anything invalidates the cache.
export function useBoardColumns(boardId: string): ColumnSpec[] {
    const [specs, setSpecs] = useState<ColumnSpec[]>([])

    const refresh = useCallback(() => {
        let cancelled = false
        loadColumns(boardId).then((loaded) => {
            if (!cancelled) {
                setSpecs(loaded)
            }
        }).catch(() => setSpecs([]))
        return () => {
            cancelled = true
        }
    }, [boardId])

    useEffect(() => {
        const cancel = refresh()
        listeners.add(refresh)

        // A session starting or ending changes what the badge counts.
        const runtime = (window as any).runtime
        const off = runtime?.EventsOn?.('acp:session', () => {
            cache.clear()
            refresh()
        })
        return () => {
            cancel()
            listeners.delete(refresh)
            off?.()
        }
    }, [refresh])

    return specs
}

type Props = {
    boardId: string
    optionId: string
    columnName: string
}

// actionIcon is a one-glyph answer to "what happens here".
function actionIcon(action: string): string {
    switch (action) {
    case 'agent':
        return '🤖'
    case 'deploy':
        return '🚀'
    case 'test':
        return '🔍'
    default:
        return ''
    }
}

const ColumnBadge = (props: Props) => {
    const {boardId, optionId, columnName} = props
    const intl = useIntl()
    const specs = useBoardColumns(boardId)
    const spec = specFor(specs, optionId, columnName)

    if (!spec || spec.action === 'none') {
        return null
    }

    const crew = spec.agents || []
    const limit = spec.maxRunning || 0
    const title = [
        actionTitle(intl, spec.action),
        crew.length > 0 ? intl.formatMessage({id: 'ColumnBadge.crew', defaultMessage: 'Worked by: {crew}'}, {crew: crew.join(', ')}) : '',
        limit > 0 ? intl.formatMessage({id: 'ColumnBadge.limit', defaultMessage: 'At once: {limit}'}, {limit}) : '',
    ].filter(Boolean).join('\n')

    return (
        <span
            className='ColumnBadge'
            title={title}
        >
            <span className='ColumnBadge__icon'>{actionIcon(spec.action)}</span>
            {crew.length > 0 &&
                <span className='ColumnBadge__crew'>{crew.length === 1 ? crew[0] : crew.length}</span>}
            {limit > 0 &&
                <span className='ColumnBadge__limit'>{limit}</span>}
        </span>
    )
}

function actionTitle(intl: {formatMessage: (d: {id: string, defaultMessage: string}) => string}, action: string): string {
    switch (action) {
    case 'agent':
        return intl.formatMessage({id: 'ColumnBadge.action-agent', defaultMessage: 'An agent works on cards dropped here'})
    case 'deploy':
        return intl.formatMessage({id: 'ColumnBadge.action-deploy', defaultMessage: 'A card dropped here is deployed'})
    case 'test':
        return intl.formatMessage({id: 'ColumnBadge.action-test', defaultMessage: 'A card dropped here is tested'})
    default:
        return ''
    }
}

export default React.memo(ColumnBadge)
