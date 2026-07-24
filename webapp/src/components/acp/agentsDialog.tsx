// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

// The Wails-generated Go bindings are PascalCase methods, not constructors.
/* eslint-disable new-cap */
import React, {useCallback, useEffect, useState} from 'react'
import {useIntl} from 'react-intl'

import {Board, IPropertyTemplate, IPropertyOption} from '../../blocks/board'
import mutator from '../../mutator'
import {Utils, IDType} from '../../utils'
import Button from '../../widgets/buttons/button'
import Dialog from '../dialog'
import {sendFlashMessage} from '../flashMessages'

import {agentBindings} from './agentReposDialog'

import './agentsDialog.scss'

// The dedicated card property whose (single-)select option names route a card
// to a registered agent. Synced by "Sync to board"; matched in resolveAgent.
const AGENT_PROPERTY_NAME = 'Agent'

type AgentEntry = {
    name: string
    kind: string
    binPath?: string
    model?: string
    prompt?: string
    env?: {[key: string]: string}
    args?: string[]
}

export function isAgentsAvailable(): boolean {
    return Boolean(agentBindings()?.ListAgents)
}

// envToText / textToEnv convert between the KEY=VALUE textarea and the env map.
function envToText(env?: {[key: string]: string}): string {
    if (!env) {
        return ''
    }
    return Object.entries(env).map(([k, v]) => `${k}=${v}`).join('\n')
}

function textToEnv(text: string): {[key: string]: string} {
    const env: {[key: string]: string} = {}
    for (const line of text.split('\n')) {
        const trimmed = line.trim()
        if (!trimmed) {
            continue
        }
        const eq = trimmed.indexOf('=')
        if (eq <= 0) {
            continue
        }
        env[trimmed.slice(0, eq).trim()] = trimmed.slice(eq + 1).trim()
    }
    return env
}

const emptyForm: AgentEntry = {name: '', kind: 'claude'}

type Props = {
    board: Board
    onClose: () => void
}

const AgentsDialog = (props: Props) => {
    const {board, onClose} = props
    const intl = useIntl()
    const bindings = agentBindings()

    const [agents, setAgents] = useState<AgentEntry[]>([])
    const [preamble, setPreamble] = useState('')
    const [form, setForm] = useState<AgentEntry | null>(null)
    const [envText, setEnvText] = useState('')
    const [argsText, setArgsText] = useState('')
    const [editingName, setEditingName] = useState<string | null>(null)
    const [error, setError] = useState('')

    const refresh = useCallback(async () => {
        if (!bindings?.ListAgents) {
            return
        }
        try {
            setAgents(JSON.parse(await bindings.ListAgents()) || [])
            if (bindings.GetAgentPreamble) {
                setPreamble(await bindings.GetAgentPreamble())
            }
        } catch (e) {
            setError(String(e))
        }
    }, [bindings])

    useEffect(() => {
        refresh()
    }, [refresh])

    const startAdd = useCallback(() => {
        setForm({...emptyForm})
        setEnvText('')
        setArgsText('')
        setEditingName(null)
        setError('')
    }, [])

    const startEdit = useCallback((agent: AgentEntry) => {
        setForm({...agent})
        setEnvText(envToText(agent.env))
        setArgsText((agent.args || []).join(' '))
        setEditingName(agent.name)
        setError('')
    }, [])

    const saveForm = useCallback(async () => {
        if (!bindings || !form) {
            return
        }
        setError('')
        const entry: AgentEntry = {
            ...form,
            name: form.name.trim(),
            env: textToEnv(envText),
            args: argsText.split(/\s+/).filter(Boolean),
        }
        try {
            if (editingName) {
                await bindings.UpdateAgent!(JSON.stringify(entry))
            } else {
                await bindings.AddAgent!(JSON.stringify(entry))
            }
            setForm(null)
            await refresh()
        } catch (e) {
            setError(String(e))
        }
    }, [bindings, form, envText, argsText, editingName, refresh])

    const removeAgent = useCallback(async (name: string) => {
        if (!bindings?.RemoveAgent) {
            return
        }
        setError('')
        try {
            await bindings.RemoveAgent(name)
            await refresh()
        } catch (e) {
            setError(String(e))
        }
    }, [bindings, refresh])

    const savePreamble = useCallback(async () => {
        if (!bindings?.SetAgentPreamble) {
            return
        }
        setError('')
        try {
            await bindings.SetAgentPreamble(preamble)
            sendFlashMessage({content: intl.formatMessage({id: 'Agents.preamble-saved', defaultMessage: 'Saved board preamble'}), severity: 'normal'})
        } catch (e) {
            setError(String(e))
        }
    }, [bindings, preamble, intl])

    // syncToBoard adds every registered agent name as an option of the board's
    // "Agent" (single-)select property, creating the property when absent.
    // Add-only: existing options (which cards may reference) are never removed.
    const syncToBoard = useCallback(async () => {
        setError('')
        try {
            const newProperties: IPropertyTemplate[] = board.cardProperties.map((p) => ({
                ...p,
                options: [...p.options],
            }))
            let property = newProperties.find((p) =>
                p.name.trim().toLowerCase() === AGENT_PROPERTY_NAME.toLowerCase() &&
                (p.type === 'select' || p.type === 'multiSelect'))
            if (!property) {
                property = {
                    id: Utils.createGuid(IDType.BlockID),
                    name: AGENT_PROPERTY_NAME,
                    type: 'select',
                    options: [],
                }
                newProperties.push(property)
            }

            const existing = new Set(property.options.map((o: IPropertyOption) => o.value.trim().toLowerCase()))
            const missing = agents.filter((a) => !existing.has(a.name.trim().toLowerCase()))
            for (const agent of missing) {
                property.options.push({
                    id: Utils.createGuid(IDType.BlockID),
                    value: agent.name,
                    color: 'propColorDefault',
                })
            }

            await mutator.updateBoardCardProperties(board.id, board.cardProperties, newProperties, 'sync agents')
            sendFlashMessage({
                content: intl.formatMessage(
                    {id: 'Agents.options-added', defaultMessage: 'Synced {count} agent option(s) to "{property}"'},
                    {count: missing.length, property: AGENT_PROPERTY_NAME},
                ),
                severity: 'normal',
            })
        } catch (e) {
            setError(String(e))
        }
    }, [board, intl, agents])

    const updateForm = (patch: Partial<AgentEntry>) => setForm((f) => (f ? {...f, ...patch} : f))

    return (
        <Dialog
            className='AgentsDialog'
            title={<span>{intl.formatMessage({id: 'Agents.title', defaultMessage: 'Agents'})}</span>}
            subtitle={<span>{intl.formatMessage({id: 'Agents.subtitle', defaultMessage: 'Register coding agents (Claude / Codex) with their own prompt, model and environment. Cards route to an agent by the "Agent" field.'})}</span>}
            onClose={onClose}
        >
            <div className='AgentsDialog__content'>
                {agents.length === 0 && !form &&
                    <div className='AgentsDialog__empty'>
                        {intl.formatMessage({id: 'Agents.empty', defaultMessage: 'No agents registered yet.'})}
                    </div>}

                {agents.map((agent) => (
                    <div
                        className='AgentsDialog__row'
                        key={agent.name}
                    >
                        <span className='AgentsDialog__name'>{agent.name}</span>
                        <span className='AgentsDialog__kind'>{agent.kind}</span>
                        <Button onClick={() => startEdit(agent)}>
                            {intl.formatMessage({id: 'Agents.edit', defaultMessage: 'Edit'})}
                        </Button>
                        <Button onClick={() => removeAgent(agent.name)}>
                            {intl.formatMessage({id: 'Agents.remove', defaultMessage: 'Remove'})}
                        </Button>
                    </div>
                ))}

                {form &&
                    <div className='AgentsDialog__form'>
                        <label>
                            {intl.formatMessage({id: 'Agents.name', defaultMessage: 'Name'})}
                            <input
                                value={form.name}
                                disabled={Boolean(editingName)}
                                placeholder={intl.formatMessage({id: 'Agents.name-placeholder', defaultMessage: 'Name (matches the "Agent" option)'})}
                                onChange={(e) => updateForm({name: e.target.value})}
                            />
                        </label>
                        <label>
                            {intl.formatMessage({id: 'Agents.kind', defaultMessage: 'Kind'})}
                            <select
                                value={form.kind}
                                onChange={(e) => updateForm({kind: e.target.value})}
                            >
                                <option value='claude'>{'Claude'}</option>
                                <option value='codex'>{'Codex'}</option>
                            </select>
                        </label>
                        <label>
                            {intl.formatMessage({id: 'Agents.model', defaultMessage: 'Model (optional)'})}
                            <input
                                value={form.model || ''}
                                onChange={(e) => updateForm({model: e.target.value})}
                            />
                        </label>
                        <label>
                            {intl.formatMessage({id: 'Agents.binPath', defaultMessage: 'Binary path (optional)'})}
                            <input
                                value={form.binPath || ''}
                                onChange={(e) => updateForm({binPath: e.target.value})}
                            />
                        </label>
                        <label>
                            {intl.formatMessage({id: 'Agents.prompt', defaultMessage: 'Agent prompt (preamble)'})}
                            <textarea
                                rows={3}
                                value={form.prompt || ''}
                                onChange={(e) => updateForm({prompt: e.target.value})}
                            />
                        </label>
                        <label>
                            {intl.formatMessage({id: 'Agents.env', defaultMessage: 'Environment (KEY=VALUE per line — e.g. CODEX_HOME, OPENAI_API_KEY)'})}
                            <textarea
                                rows={3}
                                value={envText}
                                placeholder={'CODEX_HOME=/Users/me/.codex-work'}
                                onChange={(e) => setEnvText(e.target.value)}
                            />
                        </label>
                        <label>
                            {intl.formatMessage({id: 'Agents.args', defaultMessage: 'Extra CLI args (space-separated)'})}
                            <input
                                value={argsText}
                                placeholder={'--sandbox workspace-write'}
                                onChange={(e) => setArgsText(e.target.value)}
                            />
                        </label>
                        <div className='AgentsDialog__formActions'>
                            <Button
                                emphasis='primary'
                                onClick={saveForm}
                            >
                                {intl.formatMessage({id: 'Agents.save', defaultMessage: 'Save'})}
                            </Button>
                            <Button onClick={() => setForm(null)}>
                                {intl.formatMessage({id: 'Agents.cancel', defaultMessage: 'Cancel'})}
                            </Button>
                        </div>
                    </div>}

                {!form &&
                    <div className='AgentsDialog__actions'>
                        <Button
                            emphasis='primary'
                            onClick={startAdd}
                        >
                            {intl.formatMessage({id: 'Agents.add', defaultMessage: 'Add agent…'})}
                        </Button>
                        {agents.length > 0 &&
                            <Button onClick={syncToBoard}>
                                {intl.formatMessage({id: 'Agents.sync', defaultMessage: 'Sync to board'})}
                            </Button>}
                    </div>}

                <div className='AgentsDialog__preamble'>
                    <label>
                        {intl.formatMessage({id: 'Agents.preamble', defaultMessage: 'Board preamble (prepended to every agent prompt)'})}
                        <textarea
                            rows={3}
                            value={preamble}
                            onChange={(e) => setPreamble(e.target.value)}
                        />
                    </label>
                    <Button onClick={savePreamble}>
                        {intl.formatMessage({id: 'Agents.save-preamble', defaultMessage: 'Save preamble'})}
                    </Button>
                </div>

                {error &&
                    <div className='AgentsDialog__error'>{error}</div>}
            </div>
        </Dialog>
    )
}

export default React.memo(AgentsDialog)
