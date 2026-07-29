// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

// The Wails-generated Go bindings are PascalCase methods, not constructors.
/* eslint-disable new-cap */
import React, {useCallback, useEffect, useState} from 'react'
import {useIntl} from 'react-intl'

import {Board, IPropertyTemplate} from '../../blocks/board'
import {BoardView} from '../../blocks/boardView'
import {Block} from '../../blocks/block'
import {createCard} from '../../blocks/card'
import {createTextBlock} from '../../blocks/textBlock'
import mutator from '../../mutator'
import octoClient from '../../octoClient'
import Button from '../../widgets/buttons/button'
import Dialog from '../dialog'

import {agentBindings} from './agentReposDialog'
import {Transcript, isLive, useSessionStream} from './sessionStream'

import './planningDialog.scss'

export function isPlanningAvailable(): boolean {
    return Boolean(agentBindings()?.StartPlanningSession)
}

type NamedEntry = {name: string}

// firstColumnOptionId is the option a new task lands in: the leftmost column of
// the board, which is the first visible group, or the property's first option
// when the view has not been reordered.
function firstColumnOptionId(board: Board, activeView: BoardView): {property?: IPropertyTemplate, optionId?: string} {
    const property = board.cardProperties.find((p: IPropertyTemplate) => p.id === activeView.fields.groupById)
    if (!property || property.options.length === 0) {
        return {}
    }
    const visible = activeView.fields.visibleOptionIds || []
    const firstVisible = visible.find((id) => property.options.some((o) => o.id === id))
    return {property, optionId: firstVisible || property.options[0].id}
}

// createTaskCard writes the agreed task onto the board: a card in the first
// column, with the plan as its description.
async function createTaskCard(board: Board, activeView: BoardView, title: string, description: string): Promise<Block> {
    const card = createCard()
    card.parentId = board.id
    card.boardId = board.id
    card.title = title

    const {property, optionId} = firstColumnOptionId(board, activeView)
    if (property && optionId) {
        card.fields.properties = {...card.fields.properties, [property.id]: optionId}
    }

    let created: Block | undefined
    await mutator.performAsUndoGroup(async () => {
        created = await mutator.insertBlock(card.boardId, card, 'create task from planning')
        const body = description.trim()
        if (!body) {
            return
        }
        const text = createTextBlock()
        text.parentId = created.id
        text.boardId = created.boardId
        text.title = body
        const inserted = await mutator.insertBlock(text.boardId, text, 'create task description')
        await octoClient.patchBlock(created.boardId, created.id, {updatedFields: {contentOrder: [inserted.id]}})
    })
    return created as Block
}

type Props = {
    board: Board
    activeView: BoardView
    onClose: () => void
}

const PlanningDialog = (props: Props) => {
    const {board, activeView, onClose} = props
    const intl = useIntl()
    const bindings = agentBindings()

    const [repos, setRepos] = useState<NamedEntry[]>([])
    const [agents, setAgents] = useState<NamedEntry[]>([])
    const [repoName, setRepoName] = useState('')
    const [agentName, setAgentName] = useState('')
    const [sessionId, setSessionId] = useState<string | undefined>(undefined)
    const [draft, setDraft] = useState('')
    const [busy, setBusy] = useState(false)

    // The composed task, held for review before it becomes a card.
    const [taskTitle, setTaskTitle] = useState('')
    const [taskBody, setTaskBody] = useState('')
    const [reviewing, setReviewing] = useState(false)

    const {entries, session, error, setError} = useSessionStream({sessionId})

    useEffect(() => {
        if (!bindings?.ListAgentRepos || !bindings.ListAgents) {
            return
        }
        (async () => {
            try {
                const [r, a] = await Promise.all([bindings.ListAgentRepos(), bindings.ListAgents()])
                const parsedRepos: NamedEntry[] = JSON.parse(r) || []
                const parsedAgents: NamedEntry[] = JSON.parse(a) || []
                setRepos(parsedRepos)
                setAgents(parsedAgents)

                // With a single entry there is nothing to choose.
                if (parsedRepos.length === 1) {
                    setRepoName(parsedRepos[0].name)
                }
                if (parsedAgents.length === 1) {
                    setAgentName(parsedAgents[0].name)
                }
            } catch (e) {
                setError(String(e))
            }
        })()
    }, [bindings, setError])

    // Closing the dialog must not leave the session running: nobody is watching
    // it any more, and it would sit on the repository until the idle timeout.
    useEffect(() => {
        return () => {
            if (sessionId && bindings?.CloseSession) {
                bindings.CloseSession(sessionId)
            }
        }
    }, [bindings, sessionId])

    const start = useCallback(async () => {
        if (!bindings?.StartPlanningSession) {
            return
        }
        setError('')
        setBusy(true)
        try {
            setSessionId(await bindings.StartPlanningSession(repoName, agentName))
        } catch (e) {
            setError(String(e))
        } finally {
            setBusy(false)
        }
    }, [agentName, bindings, repoName, setError])

    const send = useCallback(async () => {
        const text = draft.trim()
        if (!text || !sessionId || !bindings?.PromptSession) {
            return
        }
        setError('')
        setBusy(true)
        try {
            await bindings.PromptSession(sessionId, text)
            setDraft('')
        } catch (e) {
            setError(String(e))
        } finally {
            setBusy(false)
        }
    }, [bindings, draft, sessionId, setError])

    const answer = useCallback(async (requestId: string, optionId: string) => {
        if (!sessionId || !bindings?.AnswerPermission) {
            return
        }
        try {
            await bindings.AnswerPermission(sessionId, requestId, optionId)
        } catch (e) {
            setError(String(e))
        }
    }, [bindings, sessionId, setError])

    // Ask the agent to boil the conversation down, then show it for review
    // rather than writing it to the board straight away.
    const compose = useCallback(async () => {
        if (!sessionId || !bindings?.ComposeTask) {
            return
        }
        setError('')
        setBusy(true)
        try {
            const text = await bindings.ComposeTask(sessionId)
            const newline = text.indexOf('\n')
            setTaskTitle((newline === -1 ? text : text.slice(0, newline)).trim())
            setTaskBody(newline === -1 ? '' : text.slice(newline + 1).trim())
            setReviewing(true)
        } catch (e) {
            setError(String(e))
        } finally {
            setBusy(false)
        }
    }, [bindings, sessionId, setError])

    const createTask = useCallback(async () => {
        const title = taskTitle.trim()
        if (!title) {
            return
        }
        setBusy(true)
        try {
            await createTaskCard(board, activeView, title, taskBody)
            onClose()
        } catch (e) {
            setError(String(e))
            setBusy(false)
        }
    }, [activeView, board, onClose, setError, taskBody, taskTitle])

    const onKeyDown = useCallback((e: React.KeyboardEvent<HTMLTextAreaElement>) => {
        if (e.key === 'Enter' && !e.shiftKey) {
            e.preventDefault()
            send()
        }
    }, [send])

    if (!bindings) {
        return null
    }

    const live = isLive(session?.status)
    const started = Boolean(sessionId)

    return (
        <Dialog
            onClose={onClose}
            title={<div>{intl.formatMessage({id: 'Planning.title', defaultMessage: 'Plan a task'})}</div>}
        >
            <div className='PlanningDialog'>
                {!started &&
                    <div className='PlanningDialog__setup'>
                        <div className='PlanningDialog__hint'>
                            {intl.formatMessage({
                                id: 'Planning.hint',
                                defaultMessage: 'Talk the task through with the agent. With a repository it reads the code but changes nothing; anything else asks first.',
                            })}
                        </div>
                        <label>
                            {intl.formatMessage({id: 'Planning.repository', defaultMessage: 'Repository'})}
                            <select
                                value={repoName}
                                onChange={(e) => setRepoName(e.target.value)}
                            >
                                <option value=''>{intl.formatMessage({id: 'Planning.no-repository', defaultMessage: 'Without a repository'})}</option>
                                {repos.map((r) => (
                                    <option
                                        key={r.name}
                                        value={r.name}
                                    >{r.name}</option>
                                ))}
                            </select>
                        </label>
                        <label>
                            {intl.formatMessage({id: 'Planning.agent', defaultMessage: 'Agent'})}
                            <select
                                value={agentName}
                                onChange={(e) => setAgentName(e.target.value)}
                            >
                                <option value=''>{intl.formatMessage({id: 'Planning.choose', defaultMessage: 'Choose…'})}</option>
                                {agents.map((a) => (
                                    <option
                                        key={a.name}
                                        value={a.name}
                                    >{a.name}</option>
                                ))}
                            </select>
                        </label>
                        <Button
                            filled={true}
                            onClick={start}
                            disabled={busy || !agentName}
                        >
                            {intl.formatMessage({id: 'Planning.start', defaultMessage: 'Start planning'})}
                        </Button>
                    </div>}

                {error && <div className='SessionConsole__error'>{error}</div>}

                {started && !reviewing &&
                    <>
                        <Transcript
                            entries={entries}
                            onAnswer={answer}
                        />
                        <div className='SessionConsole__composer'>
                            <textarea
                                value={draft}
                                onChange={(e) => setDraft(e.target.value)}
                                onKeyDown={onKeyDown}
                                rows={2}
                                placeholder={intl.formatMessage({id: 'Planning.placeholder', defaultMessage: 'Describe what you want — Enter to send, Shift+Enter for a new line'})}
                            />
                            <Button
                                filled={true}
                                onClick={send}
                                disabled={busy || !draft.trim()}
                            >
                                {intl.formatMessage({id: 'SessionConsole.send', defaultMessage: 'Send'})}
                            </Button>
                        </div>
                        <div className='PlanningDialog__footer'>
                            <Button
                                onClick={compose}
                                disabled={busy || !live || entries.length === 0}
                            >
                                {intl.formatMessage({id: 'Planning.create', defaultMessage: 'Create task'})}
                            </Button>
                        </div>
                    </>}

                {reviewing &&
                    <div className='PlanningDialog__review'>
                        <label>
                            {intl.formatMessage({id: 'Planning.taskTitle', defaultMessage: 'Title'})}
                            <input
                                type='text'
                                value={taskTitle}
                                onChange={(e) => setTaskTitle(e.target.value)}
                            />
                        </label>
                        <label>
                            {intl.formatMessage({id: 'Planning.taskBody', defaultMessage: 'Description'})}
                            <textarea
                                value={taskBody}
                                onChange={(e) => setTaskBody(e.target.value)}
                                rows={10}
                            />
                        </label>
                        <div className='PlanningDialog__footer'>
                            <Button onClick={() => setReviewing(false)}>
                                {intl.formatMessage({id: 'Planning.back', defaultMessage: 'Back to chat'})}
                            </Button>
                            <Button
                                filled={true}
                                onClick={createTask}
                                disabled={busy || !taskTitle.trim()}
                            >
                                {intl.formatMessage({id: 'Planning.confirm', defaultMessage: 'Create card'})}
                            </Button>
                        </div>
                    </div>}
            </div>
        </Dialog>
    )
}

export default React.memo(PlanningDialog)
