// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

// The Wails-generated Go bindings are PascalCase methods, not constructors.
/* eslint-disable new-cap */
import React, {useCallback, useEffect, useState} from 'react'
import {useIntl} from 'react-intl'

import {Board} from '../../blocks/board'
import {UserSettings} from '../../userSettings'
import Button from '../../widgets/buttons/button'
import Dialog from '../dialog'

import {agentBindings} from './agentReposDialog'
import {AGENT_KINDS, textToServers, AdapterStatus} from './agentsDialog'

import './boardSetupWizard.scss'

// A board made from the template arrives knowing how the work is organised —
// its columns, its routes, the fields a card picks a repository and an agent
// with. What it cannot know is the machine: which agent runs, in which
// repository, where it deploys, what it tests with. That lives in the desktop
// registries, and until now the only way to find out it was empty was to drag a
// card and read the complaint afterwards.
//
// This asks for it once, in the order the work needs it.

// The playwright server, offered as the answer to "what tests with". It is the
// shape any MCP client takes, so it can also be replaced by a paste.
const BROWSER_SERVER = JSON.stringify({
    mcpServers: {
        playwright: {command: 'npx', args: ['-y', '@playwright/mcp@latest', '--headless']},
    },
}, null, 2)

type Registry = {
    agents: Array<{name: string}>
    repos: Array<{name: string, path: string}>
}

export function isBoardSetupAvailable(): boolean {
    return Boolean(agentBindings()?.ListAgentRepos)
}

// boardCarriesAutomation reports whether this board was made from a template
// that ships columns and routes — there is nothing to set up for a board that
// runs nothing.
export function boardCarriesAutomation(board?: Board): boolean {
    return Boolean(board?.properties && board.properties.acpFlows !== undefined)
}

// setupNeeded is the whole rule for showing the wizard by itself: a board that
// runs something, a machine that cannot run it yet, and nobody having said no.
export function setupNeeded(board: Board | undefined, registry: Registry | null): boolean {
    if (!board || !isBoardSetupAvailable() || !boardCarriesAutomation(board)) {
        return false
    }
    if (dismissedFor(board.id)) {
        return false
    }
    return Boolean(registry) && (registry!.agents.length === 0 || registry!.repos.length === 0)
}

// The refusal is remembered per board, so closing it once is not an answer for
// every board that follows.
export function dismissedFor(boardId: string): boolean {
    return Boolean(UserSettings.acpSetupDismissed[boardId])
}

export function rememberDismissed(boardId: string): void {
    UserSettings.setAcpSetupDismissed(boardId)
}

export async function readRegistry(): Promise<Registry | null> {
    const bindings = agentBindings()
    if (!bindings?.ListAgentRepos || !bindings.ListAgents) {
        return null
    }
    const [repos, agents] = await Promise.all([bindings.ListAgentRepos(), bindings.ListAgents()])
    return {repos: JSON.parse(repos) || [], agents: JSON.parse(agents) || []}
}

type Props = {
    board: Board
    onClose: () => void
}

const STEP_REPO = 0
const STEP_AGENT = 1
const STEP_DEPLOY = 2
const STEP_BROWSER = 3
const STEP_DONE = 4

const BoardSetupWizard = (props: Props) => {
    const {board, onClose} = props
    const intl = useIntl()
    const bindings = agentBindings()

    const [step, setStep] = useState(STEP_REPO)
    const [registry, setRegistry] = useState<Registry>({agents: [], repos: []})
    const [error, setError] = useState('')
    const [busy, setBusy] = useState(false)

    // Step 1: a repository.
    const [repoPath, setRepoPath] = useState('')
    const [repoName, setRepoName] = useState('')

    // Step 2: an agent.
    const [agentName, setAgentName] = useState('claude')
    const [agentKind, setAgentKind] = useState('claude')

    // Step 3: a Dokku host.
    const [deploy, setDeploy] = useState({name: '', sshHost: '', sshUser: '', sshKey: '', baseDomain: ''})

    // Step 4: what tests with.
    const [serversText, setServersText] = useState(BROWSER_SERVER)
    const [adapters, setAdapters] = useState<AdapterStatus[]>([])

    const refresh = useCallback(async () => {
        try {
            const loaded = await readRegistry()
            if (loaded) {
                setRegistry(loaded)
            }

            // A fresh machine is exactly where the agent's adapter is missing,
            // so the step that asks for an agent is where that has to be said.
            if (bindings?.ListAgentAdapters) {
                setAdapters(JSON.parse(await bindings.ListAgentAdapters()) || [])
            }
        } catch (e) {
            setError(String(e))
        }
    }, [bindings])

    useEffect(() => {
        refresh()
    }, [refresh])

    const adapterStatus = adapters.find((a) => a.kind === agentKind)

    // Every step does its work through the same registry calls the dialogs use,
    // and shows what Go says when it refuses.
    const run = useCallback(async (work: () => Promise<void>, next: number) => {
        setError('')
        setBusy(true)
        try {
            await work()
            await refresh()
            setStep(next)
        } catch (e) {
            setError(String(e))
        } finally {
            setBusy(false)
        }
    }, [refresh])

    const pickRepo = useCallback(async () => {
        if (!bindings?.PickDirectory) {
            return
        }
        setError('')
        try {
            const picked = await bindings.PickDirectory(intl.formatMessage({id: 'BoardSetup.pick-repo', defaultMessage: 'Choose a repository'}))
            if (picked) {
                setRepoPath(picked)
                setRepoName((current) => current || picked.split('/').filter(Boolean).pop() || '')
            }
        } catch (e) {
            setError(String(e))
        }
    }, [bindings, intl])

    const addRepo = () => run(async () => {
        await bindings!.AddAgentRepo!(repoName.trim(), repoPath)
    }, STEP_AGENT)

    const addAgent = () => run(async () => {
        await bindings!.AddAgent!(JSON.stringify({name: agentName.trim(), kind: agentKind}))
        if (bindings!.SyncAgentUsers) {
            // So the agent can be put in a card's "Assignee" like a teammate.
            await bindings!.SyncAgentUsers(board.id)
        }
    }, STEP_DEPLOY)

    const addDeploy = () => run(async () => {
        await bindings!.AddDeployTarget!(JSON.stringify({
            name: deploy.name.trim(),
            sshHost: deploy.sshHost.trim(),
            sshUser: deploy.sshUser.trim(),
            sshKey: deploy.sshKey.trim(),
            baseDomain: deploy.baseDomain.trim(),
        }))
    }, STEP_BROWSER)

    const addBrowser = () => run(async () => {
        const agent = registry.agents[0]
        if (!agent) {
            return
        }
        await bindings!.UpdateAgent!(JSON.stringify({
            name: agent.name,
            kind: agentKind,
            mcpServers: textToServers(serversText),
        }))
    }, STEP_DONE)

    const finish = useCallback(async () => {
        // Take the board's own columns and routes now, so what it can do is
        // visible without waiting for the first card to be moved.
        if (bindings?.SeedBoardAutomation) {
            try {
                await bindings.SeedBoardAutomation(board.id)
            } catch (e) {
                setError(String(e))
                return
            }
        }
        onClose()
    }, [bindings, board.id, onClose])

    const hasRepo = registry.repos.length > 0
    const hasAgent = registry.agents.length > 0

    const body = () => {
        switch (step) {
        case STEP_REPO:
            return (
                <div className='BoardSetupWizard__step'>
                    <p>{intl.formatMessage({id: 'BoardSetup.repo-why', defaultMessage: 'An agent works in a repository on your machine. A card is matched to one by its "Repositories" field, which this fills in for you.'})}</p>
                    {hasRepo &&
                        <div className='BoardSetupWizard__known'>
                            {intl.formatMessage({id: 'BoardSetup.repo-known', defaultMessage: 'Already registered: {names}'}, {names: registry.repos.map((r) => r.name).join(', ')})}
                        </div>}
                    <Button onClick={pickRepo}>
                        {intl.formatMessage({id: 'BoardSetup.choose-folder', defaultMessage: 'Choose a folder…'})}
                    </Button>
                    {repoPath &&
                        <>
                            <span className='BoardSetupWizard__path'>{repoPath}</span>
                            <label>
                                {intl.formatMessage({id: 'BoardSetup.repo-name', defaultMessage: 'Name'})}
                                <input
                                    value={repoName}
                                    onChange={(e) => setRepoName(e.target.value)}
                                />
                            </label>
                        </>}
                </div>
            )
        case STEP_AGENT:
            return (
                <div className='BoardSetupWizard__step'>
                    <p>{intl.formatMessage({id: 'BoardSetup.agent-why', defaultMessage: 'The agent that picks a card up. It has to be logged in already; here it is only given a name.'})}</p>
                    {hasAgent &&
                        <div className='BoardSetupWizard__known'>
                            {intl.formatMessage({id: 'BoardSetup.agent-known', defaultMessage: 'Already registered: {names}'}, {names: registry.agents.map((a) => a.name).join(', ')})}
                        </div>}
                    <label>
                        {intl.formatMessage({id: 'BoardSetup.agent-name', defaultMessage: 'Name'})}
                        <input
                            value={agentName}
                            onChange={(e) => setAgentName(e.target.value)}
                        />
                    </label>
                    <label>
                        {intl.formatMessage({id: 'BoardSetup.agent-kind', defaultMessage: 'Kind'})}
                        <select
                            value={agentKind}
                            onChange={(e) => setAgentKind(e.target.value)}
                        >
                            {AGENT_KINDS.map((kind) => (
                                <option
                                    key={kind.value}
                                    value={kind.value}
                                >{kind.label}</option>
                            ))}
                        </select>
                    </label>
                    {adapterStatus && !adapterStatus.ready &&
                        <div className='BoardSetupWizard__warning'>{adapterStatus.detail}</div>}
                </div>
            )
        case STEP_DEPLOY:
            return (
                <div className='BoardSetupWizard__step'>
                    <p>{intl.formatMessage({id: 'BoardSetup.deploy-why', defaultMessage: 'Where a card’s branch is published from the "Deploy" column. Skip it if nothing is deployed from here — everything else still works.'})}</p>
                    <label>
                        {intl.formatMessage({id: 'BoardSetup.deploy-name', defaultMessage: 'Name'})}
                        <input
                            value={deploy.name}
                            onChange={(e) => setDeploy({...deploy, name: e.target.value})}
                        />
                    </label>
                    <label>
                        {intl.formatMessage({id: 'BoardSetup.deploy-host', defaultMessage: 'Dokku host'})}
                        <input
                            value={deploy.sshHost}
                            onChange={(e) => setDeploy({...deploy, sshHost: e.target.value})}
                        />
                    </label>
                    <label>
                        {intl.formatMessage({id: 'BoardSetup.deploy-user', defaultMessage: 'SSH user (default dokku)'})}
                        <input
                            value={deploy.sshUser}
                            onChange={(e) => setDeploy({...deploy, sshUser: e.target.value})}
                        />
                    </label>
                    <label>
                        {intl.formatMessage({id: 'BoardSetup.deploy-key', defaultMessage: 'SSH key (absolute path, optional)'})}
                        <input
                            value={deploy.sshKey}
                            onChange={(e) => setDeploy({...deploy, sshKey: e.target.value})}
                        />
                    </label>
                    <label>
                        {intl.formatMessage({id: 'BoardSetup.deploy-domain', defaultMessage: 'Preview domain (optional)'})}
                        <input
                            value={deploy.baseDomain}
                            onChange={(e) => setDeploy({...deploy, baseDomain: e.target.value})}
                        />
                    </label>
                </div>
            )
        case STEP_BROWSER:
            return (
                <div className='BoardSetupWizard__step'>
                    <p>{intl.formatMessage({id: 'BoardSetup.browser-why', defaultMessage: 'The "To Test" column drives a browser the agent brings itself. Without a browser MCP server a test session refuses to start; the one below is the usual answer.'})}</p>
                    <textarea
                        rows={7}
                        value={serversText}
                        onChange={(e) => setServersText(e.target.value)}
                    />
                </div>
            )
        default:
            return (
                <div className='BoardSetupWizard__step'>
                    <p>{intl.formatMessage({id: 'BoardSetup.done-how', defaultMessage: 'Drag a card into "In Progress" — creating it there does not start anything, the trigger is the move. Pick a route in the card’s "Workflow" field, or the card will only be worked on where it stands.'})}</p>
                    <p className='BoardSetupWizard__hint'>
                        {intl.formatMessage({id: 'BoardSetup.done-branch', defaultMessage: 'For transitions that wait for a branch to be merged, fill the card’s "branch" property: that is the branch being watched.'})}
                    </p>
                </div>
            )
        }
    }

    const actions = () => {
        switch (step) {
        case STEP_REPO:
            return (
                <>
                    <Button
                        emphasis='primary'
                        disabled={busy || (!hasRepo && !(repoPath && repoName.trim()))}
                        onClick={() => (repoPath && repoName.trim() ? addRepo() : setStep(STEP_AGENT))}
                    >
                        {intl.formatMessage({id: 'BoardSetup.next', defaultMessage: 'Next'})}
                    </Button>
                </>
            )
        case STEP_AGENT:
            return (
                <Button
                    emphasis='primary'
                    disabled={busy || (!hasAgent && !agentName.trim())}
                    onClick={() => (agentName.trim() && !hasAgent ? addAgent() : setStep(STEP_DEPLOY))}
                >
                    {intl.formatMessage({id: 'BoardSetup.next', defaultMessage: 'Next'})}
                </Button>
            )
        case STEP_DEPLOY:
            return (
                <>
                    <Button
                        emphasis='primary'
                        disabled={busy || !deploy.name.trim() || !deploy.sshHost.trim()}
                        onClick={addDeploy}
                    >
                        {intl.formatMessage({id: 'BoardSetup.save', defaultMessage: 'Save'})}
                    </Button>
                    <Button onClick={() => setStep(STEP_BROWSER)}>
                        {intl.formatMessage({id: 'BoardSetup.skip', defaultMessage: 'Skip'})}
                    </Button>
                </>
            )
        case STEP_BROWSER:
            return (
                <>
                    <Button
                        emphasis='primary'
                        disabled={busy || !hasAgent}
                        onClick={addBrowser}
                    >
                        {intl.formatMessage({id: 'BoardSetup.save', defaultMessage: 'Save'})}
                    </Button>
                    <Button onClick={() => setStep(STEP_DONE)}>
                        {intl.formatMessage({id: 'BoardSetup.skip', defaultMessage: 'Skip'})}
                    </Button>
                </>
            )
        default:
            return (
                <Button
                    emphasis='primary'
                    onClick={finish}
                >
                    {intl.formatMessage({id: 'BoardSetup.finish', defaultMessage: 'Done'})}
                </Button>
            )
        }
    }

    const titles = [
        intl.formatMessage({id: 'BoardSetup.step-repo', defaultMessage: 'Repository'}),
        intl.formatMessage({id: 'BoardSetup.step-agent', defaultMessage: 'Agent'}),
        intl.formatMessage({id: 'BoardSetup.step-deploy', defaultMessage: 'Deploy'}),
        intl.formatMessage({id: 'BoardSetup.step-browser', defaultMessage: 'Testing'}),
        intl.formatMessage({id: 'BoardSetup.step-done', defaultMessage: 'Ready'}),
    ]

    return (
        <Dialog
            className='BoardSetupWizard'
            title={<span>{intl.formatMessage({id: 'BoardSetup.title', defaultMessage: 'Set up this board: {step}'}, {step: titles[step]})}</span>}
            subtitle={<span>{intl.formatMessage({id: 'BoardSetup.subtitle', defaultMessage: 'The board already knows how the work is organised. What it does not know is your machine.'})}</span>}
            onClose={onClose}
        >
            <div className='BoardSetupWizard__content'>
                <ol className='BoardSetupWizard__steps'>
                    {titles.map((title, i) => (
                        <li
                            key={title}
                            className={i === step ? 'BoardSetupWizard__stepName--current' : ''}
                        >{title}</li>
                    ))}
                </ol>

                {body()}

                <div className='BoardSetupWizard__actions'>{actions()}</div>

                {error &&
                    <div className='BoardSetupWizard__error'>{error}</div>}
            </div>
        </Dialog>
    )
}

export default React.memo(BoardSetupWizard)
