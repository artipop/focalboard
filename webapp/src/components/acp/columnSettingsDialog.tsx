// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

// The Wails-generated Go bindings are PascalCase methods, not constructors.
/* eslint-disable new-cap */
import React, {useCallback, useEffect, useState} from 'react'
import {useIntl} from 'react-intl'

import {IPropertyOption, IPropertyTemplate} from '../../blocks/board'
import Button from '../../widgets/buttons/button'
import Dialog from '../dialog'

import {agentBindings} from './agentReposDialog'

import './columnSettingsDialog.scss'

// What happens in one column of the board: the action a card entering it
// starts, the crew that works it, and how many of them at once. Said here once
// for the whole board, rather than repeated in every route that passes through
// the column.
export type ColumnSpec = {
    boardId?: string
    propertyId?: string
    optionId?: string
    property: string
    column: string
    action: string
    agents?: string[]
    deployName?: string
    maxRunning?: number
}

// Only the name matters here; the rest of an agent's registry entry is edited
// in the Agents dialog.
type RegisteredAgent = {name: string}

export const COLUMN_ACTIONS = ['none', 'agent', 'deploy', 'test']

export function isColumnSettingsAvailable(): boolean {
    return Boolean(agentBindings()?.ListBoardColumns)
}

// specFor is the column's saved settings, or a blank set for a column nobody
// has configured yet.
export function specFor(specs: ColumnSpec[], optionId: string, columnName: string): ColumnSpec | undefined {
    return specs.find((s) => (s.optionId && s.optionId === optionId)) ||
        specs.find((s) => !s.optionId && s.column.toLowerCase() === columnName.toLowerCase())
}

type Props = {
    boardId: string
    property: IPropertyTemplate
    option: IPropertyOption
    onClose: () => void
    onSaved?: () => void
}

const ColumnSettingsDialog = (props: Props) => {
    const {boardId, property, option, onClose, onSaved} = props
    const intl = useIntl()
    const bindings = agentBindings()

    const [form, setForm] = useState<ColumnSpec>({
        boardId,
        propertyId: property.id,
        optionId: option.id,
        property: property.name,
        column: option.value,
        action: 'none',
    })
    const [agents, setAgents] = useState<RegisteredAgent[]>([])
    const [deploys, setDeploys] = useState<Array<{name: string}>>([])
    const [error, setError] = useState('')
    const [worktrees, setWorktrees] = useState(true)

    useEffect(() => {
        const load = async () => {
            if (!bindings?.ListBoardColumns) {
                return
            }
            try {
                const specs: ColumnSpec[] = JSON.parse(await bindings.ListBoardColumns(boardId)) || []
                const saved = specFor(specs, option.id, option.value)
                if (saved) {
                    setForm({...saved, boardId, propertyId: property.id, optionId: option.id, property: property.name, column: option.value})
                }
                if (bindings.ListAgents) {
                    setAgents(JSON.parse(await bindings.ListAgents()) || [])
                }
                if (bindings.ListDeployTargets) {
                    setDeploys(JSON.parse(await bindings.ListDeployTargets()) || [])
                }
                if (bindings.GetWorktreeMode) {
                    setWorktrees((await bindings.GetWorktreeMode()) !== 'never')
                }
            } catch (e) {
                setError(String(e))
            }
        }
        load()
    }, [bindings, boardId, option.id, option.value, property.id, property.name])

    const toggleAgent = useCallback((name: string) => setForm((f) => {
        const crew = f.agents || []
        return {...f, agents: crew.includes(name) ? crew.filter((n) => n !== name) : [...crew, name]}
    }), [])

    const save = useCallback(async () => {
        if (!bindings?.SaveBoardColumn) {
            return
        }
        setError('')
        try {
            await bindings.SaveBoardColumn(JSON.stringify(form))
            onSaved?.()
            onClose()
        } catch (e) {
            setError(String(e))
        }
    }, [bindings, form, onClose, onSaved])

    const crew = form.agents || []

    return (
        <Dialog
            className='ColumnSettingsDialog'
            title={<span>{intl.formatMessage({id: 'ColumnSettings.title', defaultMessage: 'Column: {name}'}, {name: option.value})}</span>}
            subtitle={<span>{intl.formatMessage({id: 'ColumnSettings.subtitle', defaultMessage: 'What happens when a card lands here, and who does it. A flow only says where the card goes next.'})}</span>}
            onClose={onClose}
        >
            <div className='ColumnSettingsDialog__content'>
                <label>
                    {intl.formatMessage({id: 'ColumnSettings.action', defaultMessage: 'On arrival'})}
                    <select
                        value={form.action}
                        onChange={(e) => setForm({...form, action: e.target.value})}
                    >
                        {COLUMN_ACTIONS.map((a) => (
                            <option
                                key={a}
                                value={a}
                            >{actionLabel(intl, a)}</option>
                        ))}
                    </select>
                </label>

                {form.action !== 'none' &&
                    <div className='ColumnSettingsDialog__crew'>
                        <span className='ColumnSettingsDialog__label'>
                            {intl.formatMessage({id: 'ColumnSettings.crew', defaultMessage: 'Worked by'})}
                        </span>
                        {agents.length === 0 &&
                            <span className='ColumnSettingsDialog__hint'>
                                {intl.formatMessage({id: 'ColumnSettings.no-agents', defaultMessage: 'No agents registered yet — see “Agents…” in the board menu.'})}
                            </span>}
                        {agents.map((a) => (
                            <label
                                key={a.name}
                                className='ColumnSettingsDialog__agent'
                            >
                                <input
                                    type='checkbox'
                                    checked={crew.includes(a.name)}
                                    onChange={() => toggleAgent(a.name)}
                                />
                                {a.name}
                            </label>
                        ))}
                        <span className='ColumnSettingsDialog__hint'>
                            {intl.formatMessage({id: 'ColumnSettings.crew-hint', defaultMessage: 'Nobody chosen — the card decides, as before. With a crew, a card goes to whoever of them is free.'})}
                        </span>
                    </div>}

                {form.action !== 'none' &&
                    <label>
                        {intl.formatMessage({id: 'ColumnSettings.limit', defaultMessage: 'At once (0 — no limit)'})}
                        <input
                            type='number'
                            min={0}
                            value={form.maxRunning || 0}
                            onChange={(e) => setForm({...form, maxRunning: Number(e.target.value)})}
                        />
                    </label>}

                {form.action !== 'none' && (form.maxRunning || 0) !== 1 && crew.length > 1 && !worktrees &&
                    <div className='ColumnSettingsDialog__warning'>
                        {intl.formatMessage({id: 'ColumnSettings.no-worktrees', defaultMessage: 'worktreeMode is “never”, so two agents cannot work one repository at the same time: the crew will take cards one after another.'})}
                    </div>}

                {form.action === 'deploy' &&
                    <label>
                        {intl.formatMessage({id: 'ColumnSettings.deploy', defaultMessage: 'Deploy target'})}
                        <select
                            value={form.deployName || ''}
                            onChange={(e) => setForm({...form, deployName: e.target.value})}
                        >
                            <option value=''>{intl.formatMessage({id: 'ColumnSettings.deploy-default', defaultMessage: '— the card’s own —'})}</option>
                            {deploys.map((d) => (
                                <option
                                    key={d.name}
                                    value={d.name}
                                >{d.name}</option>
                            ))}
                        </select>
                    </label>}

                <div className='ColumnSettingsDialog__actions'>
                    <Button
                        emphasis='primary'
                        onClick={save}
                    >
                        {intl.formatMessage({id: 'ColumnSettings.save', defaultMessage: 'Save'})}
                    </Button>
                    <Button onClick={onClose}>
                        {intl.formatMessage({id: 'ColumnSettings.cancel', defaultMessage: 'Cancel'})}
                    </Button>
                </div>

                {error &&
                    <div className='ColumnSettingsDialog__error'>{error}</div>}
            </div>
        </Dialog>
    )
}

// actionLabel names what a column does, in the reader's language.
function actionLabel(intl: {formatMessage: (d: {id: string, defaultMessage: string}) => string}, action: string): string {
    switch (action) {
    case 'agent':
        return intl.formatMessage({id: 'ColumnSettings.action-agent', defaultMessage: 'an agent works on the card'})
    case 'deploy':
        return intl.formatMessage({id: 'ColumnSettings.action-deploy', defaultMessage: 'deploy the card’s branch'})
    case 'test':
        return intl.formatMessage({id: 'ColumnSettings.action-test', defaultMessage: 'test the preview'})
    default:
        return intl.formatMessage({id: 'ColumnSettings.action-none', defaultMessage: 'nothing'})
    }
}

export default React.memo(ColumnSettingsDialog)
