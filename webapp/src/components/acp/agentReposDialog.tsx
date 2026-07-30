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

import './agentReposDialog.scss'

// The dedicated card property the registry syncs into. Cards are mapped to a
// repository by an option of this (multiSelect) property; it also exists in the
// "My Project Tasks" board template.
const REPO_PROPERTY_NAME = 'Repositories'

type AgentRepo = {
    name: string
    path: string
}

// agentBindings returns the Wails-injected ACP bindings, or undefined in
// browser/plugin deployments (mirrors the webSocketBaseURL guard pattern).
export function agentBindings() {
    return (window as unknown as import('../../types').IAppWindow).go?.main?.App
}

export function isAgentReposAvailable(): boolean {
    return Boolean(agentBindings()?.ListAgentRepos)
}

type Props = {
    board: Board
    onClose: () => void
}

const AgentReposDialog = (props: Props) => {
    const {board, onClose} = props
    const intl = useIntl()
    const bindings = agentBindings()

    const [repos, setRepos] = useState<AgentRepo[]>([])
    const [pendingPath, setPendingPath] = useState('')
    const [pendingName, setPendingName] = useState('')
    const [error, setError] = useState('')

    // syncToBoard mirrors the registry into the board's "Repositories" property,
    // creating that multiSelect property when the board has none. Add-only:
    // existing options (which cards may reference) are never removed, and a
    // board that already lists every repository is left untouched — this runs
    // on its own, so it must not churn the board or the undo history.
    const syncToBoard = useCallback(async (registry: AgentRepo[]) => {
        if (registry.length === 0) {
            return
        }
        const property = board.cardProperties.find((p: IPropertyTemplate) =>
            p.name.trim().toLowerCase() === REPO_PROPERTY_NAME.toLowerCase() &&
            (p.type === 'select' || p.type === 'multiSelect'))
        const existing = new Set((property?.options || []).map((o: IPropertyOption) => o.value.trim().toLowerCase()))
        const missing = registry.filter((r) => !existing.has(r.name.trim().toLowerCase()))
        if (property && missing.length === 0) {
            return
        }

        try {
            const newProperties: IPropertyTemplate[] = board.cardProperties.map((p) => ({
                ...p,
                options: [...p.options],
            }))
            let target = newProperties.find((p) =>
                p.name.trim().toLowerCase() === REPO_PROPERTY_NAME.toLowerCase() &&
                (p.type === 'select' || p.type === 'multiSelect'))
            if (!target) {
                target = {
                    id: Utils.createGuid(IDType.BlockID),
                    name: REPO_PROPERTY_NAME,
                    type: 'multiSelect',
                    options: [],
                }
                newProperties.push(target)
            }
            for (const repo of missing) {
                target.options.push({
                    id: Utils.createGuid(IDType.BlockID),
                    value: repo.name,
                    color: 'propColorDefault',
                })
            }

            await mutator.updateBoardCardProperties(board.id, board.cardProperties, newProperties, 'sync repositories')
            if (missing.length > 0) {
                sendFlashMessage({
                    content: intl.formatMessage(
                        {id: 'AgentRepos.options-added', defaultMessage: 'Added {count} repository option(s) to "{property}"'},
                        {count: missing.length, property: REPO_PROPERTY_NAME},
                    ),
                    severity: 'normal',
                })
            }
        } catch (e) {
            setError(String(e))
        }
    }, [board, intl])

    const refresh = useCallback(async () => {
        if (!bindings) {
            return
        }
        let registry: AgentRepo[] = []
        try {
            registry = JSON.parse(await bindings.ListAgentRepos()) || []
            setRepos(registry)
        } catch (e) {
            setError(String(e))
            return
        }

        // The board's "Repositories" field is kept in step on its own, so a
        // registered repository is selectable on a card without a second step.
        await syncToBoard(registry)
    }, [bindings, syncToBoard])

    useEffect(() => {
        refresh()
    }, [refresh])

    const pickDirectory = useCallback(async () => {
        if (!bindings) {
            return
        }
        setError('')
        try {
            const path = await bindings.PickDirectory(intl.formatMessage({id: 'AgentRepos.pick-title', defaultMessage: 'Choose a local git repository'}))
            if (path) {
                setPendingPath(path)
                setPendingName(path.split('/').filter(Boolean).pop() || '')
            }
        } catch (e) {
            setError(String(e))
        }
    }, [bindings, intl])

    const confirmAdd = useCallback(async () => {
        if (!bindings || !pendingPath) {
            return
        }
        setError('')
        try {
            await bindings.AddAgentRepo(pendingName.trim(), pendingPath)
            setPendingPath('')
            setPendingName('')
            await refresh()
        } catch (e) {
            setError(String(e))
        }
    }, [bindings, pendingName, pendingPath, refresh])

    const removeRepo = useCallback(async (name: string) => {
        if (!bindings) {
            return
        }
        setError('')
        try {
            await bindings.RemoveAgentRepo(name)
            await refresh()
        } catch (e) {
            setError(String(e))
        }
    }, [bindings, refresh])

    return (
        <Dialog
            className='AgentReposDialog'
            title={<span>{intl.formatMessage({id: 'AgentRepos.title', defaultMessage: 'Repositories'})}</span>}
            subtitle={<span>{intl.formatMessage({id: 'AgentRepos.subtitle', defaultMessage: 'Local git repositories an agent can work in. A card is matched to one by its "Repositories" field.'})}</span>}
            onClose={onClose}
        >
            <div className='AgentReposDialog__content'>
                {repos.length === 0 && !pendingPath &&
                    <div className='AgentReposDialog__empty'>
                        {intl.formatMessage({id: 'AgentRepos.empty', defaultMessage: 'No repositories registered yet.'})}
                    </div>}

                {repos.map((repo) => (
                    <div
                        className='AgentReposDialog__row'
                        key={repo.name}
                    >
                        <span className='AgentReposDialog__name'>{repo.name}</span>
                        <span className='AgentReposDialog__path'>{repo.path}</span>
                        <Button
                            onClick={() => removeRepo(repo.name)}
                            title={intl.formatMessage({id: 'AgentRepos.remove', defaultMessage: 'Remove'})}
                        >
                            {intl.formatMessage({id: 'AgentRepos.remove', defaultMessage: 'Remove'})}
                        </Button>
                    </div>
                ))}

                {pendingPath &&
                    <div className='AgentReposDialog__row AgentReposDialog__row--pending'>
                        <input
                            className='AgentReposDialog__nameInput'
                            value={pendingName}
                            placeholder={intl.formatMessage({id: 'AgentRepos.name-placeholder', defaultMessage: 'Name (matches the "Repositories" option)'})}
                            onChange={(e) => setPendingName(e.target.value)}
                        />
                        <span className='AgentReposDialog__path'>{pendingPath}</span>
                        <Button
                            emphasis='primary'
                            onClick={confirmAdd}
                        >
                            {intl.formatMessage({id: 'AgentRepos.add', defaultMessage: 'Add'})}
                        </Button>
                        <Button onClick={() => setPendingPath('')}>
                            {intl.formatMessage({id: 'AgentRepos.cancel', defaultMessage: 'Cancel'})}
                        </Button>
                    </div>}

                {!pendingPath &&
                    <div className='AgentReposDialog__actions'>
                        <Button
                            emphasis='primary'
                            onClick={pickDirectory}
                        >
                            {intl.formatMessage({id: 'AgentRepos.add-repository', defaultMessage: 'Add repository…'})}
                        </Button>
                    </div>}

                {repos.length > 0 &&
                    <div className='AgentReposDialog__sync'>
                        <span>
                            {intl.formatMessage({id: 'AgentRepos.sync-hint', defaultMessage: 'Every repository above is an option of the board’s "Repositories" field, so a card picks one there.'})}
                        </span>
                    </div>}

                {error &&
                    <div className='AgentReposDialog__error'>{error}</div>}
            </div>
        </Dialog>
    )
}

export default React.memo(AgentReposDialog)
