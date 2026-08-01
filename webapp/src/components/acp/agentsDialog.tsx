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
import ProxiesPanel, {ProxyEntry, isProxiesAvailable} from './proxiesPanel'

import './agentsDialog.scss'

// The dedicated card property whose (single-)select option names route a card
// to a registered agent. Synced by "Sync to board"; matched in resolveAgent.
const AGENT_PROPERTY_NAME = 'Agent'

// The standard MCP client shape, so a server can be pasted straight from its
// README: a name mapped to the command that starts it.
type AgentMCPServer = {
    command?: string
    args?: string[]
    env?: {[key: string]: string}
    type?: string
    url?: string
}

type AgentMCPServers = {[name: string]: AgentMCPServer}

// What the field expects, shown when it is empty: the browser server a test
// session needs, in the form its own README gives it.
const mcpServersPlaceholder = JSON.stringify({
    mcpServers: {
        playwright: {command: 'npx', args: ['-y', '@playwright/mcp@latest', '--headless', '--browser', 'chrome']},
    },
}, null, 2)

type AgentEntry = {
    name: string
    kind: string
    binPath?: string
    model?: string
    prompt?: string
    env?: {[key: string]: string}
    args?: string[]
    command?: string[]
    mcpServers?: AgentMCPServers
    proxyName?: string
}

// Launch command placeholders per kind: for claude/codex the command wraps the
// CLI (a proxy launcher, a per-account shim); the ACP kinds spawn it directly.
const commandPlaceholders: {[kind: string]: string} = {
    claude: 'proxychains4 -q -f /etc/myproxy.conf claude',
    codex: 'proxychains4 -q -f /etc/myproxy.conf codex',
    antigravity: 'antigravity --acp',
    copilot: 'copilot --acp',
    junie: 'junie --acp=true',
    acp: 'gemini --acp',
}

// The agent kinds the manager knows, in the order they are offered. Exported
// because the setup wizard asks the same question.
export const AGENT_KINDS = [
    {value: 'claude', label: 'Claude'},
    {value: 'codex', label: 'Codex'},
    {value: 'antigravity', label: 'Antigravity'},
    {value: 'copilot', label: 'GitHub Copilot'},
    {value: 'junie', label: 'JetBrains Junie'},
    {value: 'acp', label: 'ACP (other)'},
]

export function isAgentsAvailable(): boolean {
    return Boolean(agentBindings()?.ListAgents)
}

// envToText / textToEnv convert between the KEY=VALUE textarea and the env map.
// Exported: the deploy-target dialog edits an env map the same way.
export function envToText(env?: {[key: string]: string}): string {
    if (!env) {
        return ''
    }
    return Object.entries(env).map(([k, v]) => `${k}=${v}`).join('\n')
}

// serversToText / textToServers convert between the textarea and the map, in
// the JSON every MCP client uses. The mcpServers wrapper is written on the way
// out and accepted but not required on the way in, which is what lets a block
// be pasted from a server's README as it is. Invalid JSON throws: the caller
// says so instead of silently saving an empty list.
export function serversToText(servers?: AgentMCPServers): string {
    if (!servers || Object.keys(servers).length === 0) {
        return ''
    }
    return JSON.stringify({mcpServers: servers}, null, 2)
}

export function textToServers(text: string): AgentMCPServers {
    if (!text.trim()) {
        return {}
    }
    const parsed = JSON.parse(text)
    if (!parsed || typeof parsed !== 'object') {
        throw new Error('mcpServers must be an object')
    }
    const servers = (!Array.isArray(parsed) && parsed.mcpServers !== undefined) ? parsed.mcpServers : parsed
    if (!servers || typeof servers !== 'object') {
        throw new Error('mcpServers must be an object')
    }

    // Some clients list the servers instead of keying them by name, and that is
    // what somebody copying from one of them will paste. The config file reads
    // both shapes, so the dialog does too.
    if (Array.isArray(servers)) {
        const named: AgentMCPServers = {}
        for (const entry of servers) {
            const {name, ...server} = entry || {}
            if (!name) {
                throw new Error('every server in the list needs a "name"')
            }
            named[name] = server
        }
        return named
    }
    return servers
}

export function textToEnv(text: string): {[key: string]: string} {
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

// splitArgv / joinArgv convert between the single-line argv inputs and the
// string arrays sent to Go, honouring quotes so paths with spaces survive
// (a config under "~/Library/Application Support/…" is one argument).
function splitArgv(text: string): string[] {
    const argv: string[] = []
    const token = /"([^"]*)"|'([^']*)'|(\S+)/g
    let match = token.exec(text)
    while (match) {
        const [, doubleQuoted, singleQuoted, bare] = match
        const arg = [doubleQuoted, singleQuoted, bare].find((v) => v !== undefined)
        argv.push(arg as string)
        match = token.exec(text)
    }
    return argv
}

function joinArgv(argv?: string[]): string {
    const whitespace = (/\s/)
    return (argv || []).map((a) => (whitespace.test(a) ? `"${a}"` : a)).join(' ')
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
    const [proxies, setProxies] = useState<ProxyEntry[]>([])
    const [systemPrompt, setSystemPrompt] = useState('')
    const [form, setForm] = useState<AgentEntry | null>(null)
    const [envText, setEnvText] = useState('')
    const [serversText, setServersText] = useState('')
    const [argsText, setArgsText] = useState('')
    const [commandText, setCommandText] = useState('')
    const [editingName, setEditingName] = useState<string | null>(null)
    const [error, setError] = useState('')

    const refresh = useCallback(async () => {
        if (!bindings?.ListAgents) {
            return
        }
        try {
            setAgents(JSON.parse(await bindings.ListAgents()) || [])
            if (bindings.ListProxies) {
                setProxies(JSON.parse(await bindings.ListProxies()) || [])
            }
            if (bindings.GetAgentSystemPrompt) {
                setSystemPrompt(await bindings.GetAgentSystemPrompt())
            }
        } catch (e) {
            setError(String(e))
            return
        }

        // Every registered agent is kept assignable on the board being looked
        // at: the accounts and memberships are created here, so a person field
        // can name an agent. Idempotent, so it rides along with every refresh —
        // opening the dialog, adding, editing or removing an agent — and stays
        // quiet unless an account actually appears. A failure is reported but
        // never hides the registry itself.
        if (!bindings.SyncAgentUsers) {
            return
        }
        try {
            const synced = (JSON.parse(await bindings.SyncAgentUsers(board.id)) || []) as Array<{created?: boolean}>
            const created = synced.filter((u) => u.created).length
            if (created > 0) {
                sendFlashMessage({
                    content: intl.formatMessage(
                        {id: 'Agents.users-synced', defaultMessage: 'Created {created} agent account(s); agents can now be assigned to cards'},
                        {created},
                    ),
                    severity: 'normal',
                })
            }
        } catch (e) {
            setError(String(e))
        }
    }, [bindings, board.id, intl])

    useEffect(() => {
        refresh()
    }, [refresh])

    const startAdd = useCallback(() => {
        setForm({...emptyForm})
        setEnvText('')
        setServersText('')
        setArgsText('')
        setCommandText('')
        setEditingName(null)
        setError('')
    }, [])

    const startEdit = useCallback((agent: AgentEntry) => {
        setForm({...agent})
        setEnvText(envToText(agent.env))
        setServersText(serversToText(agent.mcpServers))
        setArgsText(joinArgv(agent.args))
        setCommandText(joinArgv(agent.command))
        setEditingName(agent.name)
        setError('')
    }, [])

    const saveForm = useCallback(async () => {
        if (!bindings || !form) {
            return
        }
        setError('')
        let mcpServers: AgentMCPServers
        try {
            mcpServers = textToServers(serversText)
        } catch (e) {
            setError(intl.formatMessage({id: 'Agents.mcp-servers-invalid', defaultMessage: 'MCP servers must be valid JSON: a server name mapped to its command and args, the same block any MCP client takes.'}))
            return
        }
        const entry: AgentEntry = {
            ...form,
            name: form.name.trim(),
            env: textToEnv(envText),
            args: splitArgv(argsText),
            command: splitArgv(commandText),
            mcpServers,
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
    }, [bindings, form, intl, envText, serversText, argsText, commandText, editingName, refresh])

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

    const saveSystemPrompt = useCallback(async () => {
        if (!bindings?.SetAgentSystemPrompt) {
            return
        }
        setError('')
        try {
            await bindings.SetAgentSystemPrompt(systemPrompt)
            sendFlashMessage({content: intl.formatMessage({id: 'Agents.system-prompt-saved', defaultMessage: 'Saved board system prompt'}), severity: 'normal'})
        } catch (e) {
            setError(String(e))
        }
    }, [bindings, systemPrompt, intl])

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
            subtitle={<span>{intl.formatMessage({id: 'Agents.subtitle', defaultMessage: 'Register coding agents (Claude, Codex, Antigravity, GitHub Copilot, JetBrains Junie or any other ACP agent) with their own prompt, model, launch command, environment and proxy. Cards route to an agent by their assignee or the "Agent" field.'})}</span>}
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
                                {AGENT_KINDS.map((kind) => (
                                    <option
                                        key={kind.value}
                                        value={kind.value}
                                    >{kind.label}</option>
                                ))}
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
                            {intl.formatMessage({id: 'Agents.command', defaultMessage: 'Launch command (argv) — overrides the binary path; wrap the CLI to route it through a proxy. Required for "ACP (other)".'})}
                            <input
                                value={commandText}
                                placeholder={commandPlaceholders[form.kind] || ''}
                                onChange={(e) => setCommandText(e.target.value)}
                            />
                        </label>
                        <label>
                            {intl.formatMessage({id: 'Agents.prompt', defaultMessage: 'Agent system prompt'})}
                            <textarea
                                rows={3}
                                value={form.prompt || ''}
                                onChange={(e) => updateForm({prompt: e.target.value})}
                            />
                        </label>
                        <label>
                            {intl.formatMessage({id: 'Agents.proxyName', defaultMessage: 'Proxy configuration'})}
                            <select
                                value={form.proxyName || ''}
                                onChange={(e) => updateForm({proxyName: e.target.value})}
                            >
                                <option value=''>
                                    {intl.formatMessage({id: 'Agents.proxy-none', defaultMessage: 'No proxy (inherit the app environment)'})}
                                </option>
                                {proxies.map((p) => (
                                    <option
                                        key={p.name}
                                        value={p.name}
                                    >
                                        {p.proxy ? `${p.name} — ${p.proxy}` : p.name}
                                    </option>
                                ))}
                            </select>
                        </label>
                        {proxies.length === 0 &&
                            <div className='AgentsDialog__hint'>
                                {intl.formatMessage({id: 'Agents.proxy-hint', defaultMessage: 'Configurations are added under "Proxy configurations" at the bottom of this dialog.'})}
                            </div>}
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
                            {intl.formatMessage({id: 'Agents.mcp-servers', defaultMessage: 'MCP servers (the JSON any MCP client takes) — offered to this agent in every session'})}
                            <textarea
                                rows={7}
                                value={serversText}
                                placeholder={mcpServersPlaceholder}
                                onChange={(e) => setServersText(e.target.value)}
                            />
                        </label>
                        <div className='AgentsDialog__hint'>
                            {intl.formatMessage({id: 'Agents.mcp-servers-hint', defaultMessage: 'Their tools run without asking: wiring a server here is consent to use it. A browser server (Playwright, say) is what the "To Test" column runs on.'})}
                        </div>
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

                {!form && agents.length > 0 && Boolean(bindings?.SyncAgentUsers) &&
                    <div className='AgentsDialog__hint'>
                        {intl.formatMessage({id: 'Agents.assignable-hint', defaultMessage: 'Every agent above is a member of this board under its own name, so you can pick it in a person field such as "Assignee".'})}
                    </div>}

                <div className='AgentsDialog__systemPrompt'>
                    <label>
                        {intl.formatMessage({id: 'Agents.system-prompt', defaultMessage: 'Board system prompt (prepended to every agent prompt)'})}
                        <textarea
                            rows={3}
                            value={systemPrompt}
                            onChange={(e) => setSystemPrompt(e.target.value)}
                        />
                    </label>
                    <Button onClick={saveSystemPrompt}>
                        {intl.formatMessage({id: 'Agents.save-system-prompt', defaultMessage: 'Save system prompt'})}
                    </Button>
                </div>

                {/* Proxies exist only to be referenced by an agent, so they live
                    here, folded away until someone needs one. */}
                {isProxiesAvailable() &&
                    <details className='AgentsDialog__proxies'>
                        <summary>
                            {intl.formatMessage({id: 'Proxies.title', defaultMessage: 'Proxy configurations'})}
                        </summary>
                        <ProxiesPanel onChange={refresh}/>
                    </details>}

                {error &&
                    <div className='AgentsDialog__error'>{error}</div>}
            </div>
        </Dialog>
    )
}

export default React.memo(AgentsDialog)
