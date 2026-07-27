// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

// The Wails-generated Go bindings are PascalCase methods, not constructors.
/* eslint-disable new-cap */
import React, {useCallback, useEffect, useRef, useState} from 'react'
import {useIntl} from 'react-intl'

import Button from '../../widgets/buttons/button'

import {agentBindings} from './agentReposDialog'

import './sessionConsole.scss'

// Session lifecycle, mirroring acp.SessionStatus. Only "done"/"failed"/
// "cancelled" are terminal; "idle" is a live session waiting for the next turn.
type SessionStatus = 'queued' | 'running' | 'idle' | 'waiting_permission' | 'done' | 'failed' | 'cancelled'

const liveStatuses: SessionStatus[] = ['queued', 'running', 'idle', 'waiting_permission']

function isLive(status?: SessionStatus): boolean {
    return Boolean(status && liveStatuses.includes(status))
}

type PermissionOption = {
    optionId: string
    name: string
    kind: string
}

// One rendered line of the transcript.
type Entry =
    | {kind: 'text', text: string, thought?: boolean}
    | {kind: 'prompt', text: string}
    | {kind: 'error', text: string}
    | {kind: 'tool', toolCallId: string, title?: string, status?: string}
    | {kind: 'permission', requestId?: string, tool?: string, title?: string, options?: PermissionOption[], decision?: string}

type SessionRecord = {
    id: string
    status: SessionStatus
    errorText?: string
    startedAt?: string
}

type StoredEvent = {
    sessionId: string
    kind: string
    payload: any
}

export function isSessionConsoleAvailable(): boolean {
    return Boolean(agentBindings()?.GetCardSessions)
}

// appendEntry merges consecutive text of the same kind into one paragraph, so a
// token-by-token stream does not become hundreds of DOM nodes.
function appendEntry(entries: Entry[], next: Entry): Entry[] {
    const last = entries[entries.length - 1]
    if (next.kind === 'text' && last?.kind === 'text' && Boolean(last.thought) === Boolean(next.thought)) {
        return [...entries.slice(0, -1), {...last, text: last.text + next.text}]
    }
    if (next.kind === 'tool' && last?.kind === 'tool' && last.toolCallId === next.toolCallId) {
        return [...entries.slice(0, -1), {...last, ...next, title: next.title || last.title}]
    }
    return [...entries, next]
}

// entriesFromStored replays a persisted event log into transcript entries.
function entriesFromStored(events: StoredEvent[]): Entry[] {
    let entries: Entry[] = []
    for (const ev of events) {
        const p = ev.payload || {}
        switch (ev.kind) {
        case 'chunk':
            entries = appendEntry(entries, {kind: 'text', text: p.text || ''})
            break
        case 'thought':
            entries = appendEntry(entries, {kind: 'text', text: p.text || '', thought: true})
            break
        case 'prompt':
            entries = appendEntry(entries, {kind: 'prompt', text: p.text || ''})
            break
        case 'error':
            entries = appendEntry(entries, {kind: 'error', text: p.text || ''})
            break
        case 'tool_call':
            entries = appendEntry(entries, {kind: 'tool', toolCallId: p.toolCallId, title: p.title, status: p.status})
            break
        case 'tool_update':
            entries = appendEntry(entries, {kind: 'tool', toolCallId: p.toolCallId, status: p.status})
            break
        case 'permission':
            // A prompt that was already answered is replayed as its decision.
            entries = appendEntry(entries, {
                kind: 'permission',
                requestId: p.pending ? p.requestId : undefined,
                tool: p.tool,
                title: p.title,
                options: p.pending ? p.options : undefined,
                decision: p.decision,
            })
            break
        default:
            break
        }
    }
    return entries
}

type Props = {
    cardId: string
}

const SessionConsole = (props: Props) => {
    const {cardId} = props
    const intl = useIntl()
    const bindings = agentBindings()

    const [session, setSession] = useState<SessionRecord | null>(null)
    const [entries, setEntries] = useState<Entry[]>([])
    const [draft, setDraft] = useState('')
    const [error, setError] = useState('')
    const [busy, setBusy] = useState(false)
    const scrollRef = useRef<HTMLDivElement>(null)

    // The live session id drives attach/detach; a ref keeps the unmount cleanup
    // from capturing a stale value.
    const liveSessionId = useRef<string | null>(null)

    // attachTo keeps the backend's "a human is watching" count in step with what
    // this console is showing. It matters beyond bookkeeping: an unattached
    // session answers permission prompts by policy instead of asking the user.
    const attachTo = useCallback(async (id: string | null) => {
        const prev = liveSessionId.current
        if (prev === id) {
            return
        }
        if (prev && bindings?.DetachSession) {
            bindings.DetachSession(prev)
        }
        liveSessionId.current = id
        if (id && bindings?.AttachSession) {
            await bindings.AttachSession(id)
        }
    }, [bindings])

    const hydrate = useCallback(async () => {
        if (!bindings?.GetCardSessions) {
            return
        }
        try {
            const raw = JSON.parse(await bindings.GetCardSessions(cardId))
            const sessions: SessionRecord[] = raw.sessions || []
            const latest = sessions[0] || null
            setSession(latest)
            const events: StoredEvent[] = (raw.events || []).filter((e: StoredEvent) => !latest || e.sessionId === latest.id)
            setEntries(entriesFromStored(events))
            await attachTo(latest && isLive(latest.status) ? latest.id : null)
        } catch (e) {
            setError(String(e))
        }
    }, [attachTo, bindings, cardId])

    useEffect(() => {
        hydrate()
    }, [hydrate])

    // Detach when the card closes, so an unattended idle session does not keep
    // holding its repository.
    useEffect(() => {
        return () => {
            const id = liveSessionId.current
            if (id && bindings?.DetachSession) {
                bindings.DetachSession(id)
            }
        }
    }, [bindings])

    useEffect(() => {
        const runtime = (window as any).runtime
        if (!runtime?.EventsOn) {
            return undefined
        }
        const mine = (payload: any) => payload && payload.cardId === cardId

        const offs = [
            runtime.EventsOn('acp:session', (payload: any) => {
                if (!mine(payload)) {
                    return
                }
                setSession((prev) => ({
                    id: payload.sessionId,
                    status: payload.status,
                    errorText: payload.error || prev?.errorText,
                }))
                if (payload.error) {
                    setError(payload.error)
                }

                // A session that starts while the card is already open is only
                // ever announced here, so this is where it gets attached.
                attachTo(isLive(payload.status) ? payload.sessionId : null)
            }),
            runtime.EventsOn('acp:chunk', (payload: any) => {
                if (mine(payload)) {
                    setEntries((prev) => appendEntry(prev, {kind: 'text', text: payload.text, thought: payload.thought}))
                }
            }),
            runtime.EventsOn('acp:prompt', (payload: any) => {
                if (mine(payload)) {
                    setEntries((prev) => appendEntry(prev, {kind: 'prompt', text: payload.text}))
                }
            }),
            runtime.EventsOn('acp:tool', (payload: any) => {
                if (mine(payload)) {
                    setEntries((prev) => appendEntry(prev, {
                        kind: 'tool',
                        toolCallId: payload.toolCallId,
                        title: payload.title,
                        status: payload.status,
                    }))
                }
            }),
            runtime.EventsOn('acp:permission', (payload: any) => {
                if (!mine(payload)) {
                    return
                }
                setEntries((prev) => {
                    // An answered prompt replaces the pending one it resolves.
                    if (!payload.pending) {
                        const idx = prev.findIndex((e) => e.kind === 'permission' && e.requestId)
                        if (idx >= 0) {
                            const next = [...prev]
                            next[idx] = {...prev[idx], requestId: undefined, options: undefined, decision: payload.decision}
                            return next
                        }
                    }
                    return appendEntry(prev, {
                        kind: 'permission',
                        requestId: payload.requestId,
                        tool: payload.tool,
                        title: payload.title,
                        options: payload.options,
                        decision: payload.decision,
                    })
                })
            }),
        ]
        return () => offs.forEach((off) => typeof off === 'function' && off())
    }, [attachTo, cardId])

    // Follow the stream. Assigning scrollTop rather than calling scrollTo keeps
    // this working under jsdom, which implements only the property.
    useEffect(() => {
        const el = scrollRef.current
        if (el) {
            el.scrollTop = el.scrollHeight
        }
    }, [entries])

    const openSession = useCallback(async () => {
        if (!bindings?.StartCardSession) {
            return
        }
        setError('')
        setBusy(true)
        try {
            const id = await bindings.StartCardSession(cardId)

            // StartCardSession already counts this console as attached, so this
            // records the id without attaching a second time.
            liveSessionId.current = id
            setSession({id, status: 'queued'})
            setEntries([])
        } catch (e) {
            setError(String(e))
        } finally {
            setBusy(false)
        }
    }, [bindings, cardId])

    const send = useCallback(async () => {
        const text = draft.trim()
        if (!text || !session || !bindings?.PromptSession) {
            return
        }
        setError('')
        setBusy(true)
        try {
            await bindings.PromptSession(session.id, text)
            setDraft('')
        } catch (e) {
            setError(String(e))
        } finally {
            setBusy(false)
        }
    }, [bindings, draft, session])

    const answer = useCallback(async (requestId: string, optionId: string) => {
        if (!session || !bindings?.AnswerPermission) {
            return
        }
        setError('')
        try {
            await bindings.AnswerPermission(session.id, requestId, optionId)
        } catch (e) {
            setError(String(e))
        }
    }, [bindings, session])

    const cancel = useCallback(async () => {
        if (!bindings?.CancelSession) {
            return
        }
        await bindings.CancelSession(cardId)
    }, [bindings, cardId])

    const close = useCallback(async () => {
        if (!session || !bindings?.CloseSession) {
            return
        }
        liveSessionId.current = null
        try {
            await bindings.CloseSession(session.id)
        } catch (e) {
            setError(String(e))
        }
    }, [bindings, session])

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
    const working = session?.status === 'running' || session?.status === 'waiting_permission'

    return (
        <div className='SessionConsole'>
            <div className='SessionConsole__header'>
                <span className='SessionConsole__title'>
                    {intl.formatMessage({id: 'SessionConsole.title', defaultMessage: 'Agent session'})}
                </span>
                {session &&
                    <span className={`SessionConsole__status SessionConsole__status--${session.status}`}>
                        {session.status}
                    </span>}
                <div className='SessionConsole__actions'>
                    {!live &&
                        <Button
                            onClick={openSession}
                            disabled={busy}
                        >
                            {intl.formatMessage({id: 'SessionConsole.open', defaultMessage: 'Open session'})}
                        </Button>}
                    {working &&
                        <Button onClick={cancel}>
                            {intl.formatMessage({id: 'SessionConsole.cancel', defaultMessage: 'Cancel turn'})}
                        </Button>}
                    {live &&
                        <Button onClick={close}>
                            {intl.formatMessage({id: 'SessionConsole.close', defaultMessage: 'Close session'})}
                        </Button>}
                </div>
            </div>

            {error && <div className='SessionConsole__error'>{error}</div>}

            {(entries.length > 0 || live) &&
                <div
                    ref={scrollRef}
                    className='SessionConsole__transcript'
                >
                    {entries.map((entry, i) => (
                        <ConsoleEntry
                            key={i}
                            entry={entry}
                            onAnswer={answer}
                        />
                    ))}
                </div>}

            {live &&
                <div className='SessionConsole__composer'>
                    <textarea
                        value={draft}
                        onChange={(e) => setDraft(e.target.value)}
                        onKeyDown={onKeyDown}
                        rows={2}
                        placeholder={intl.formatMessage({id: 'SessionConsole.placeholder', defaultMessage: 'Message the agent — Enter to send, Shift+Enter for a new line'})}
                    />
                    <Button
                        filled={true}
                        onClick={send}
                        disabled={busy || !draft.trim()}
                    >
                        {intl.formatMessage({id: 'SessionConsole.send', defaultMessage: 'Send'})}
                    </Button>
                </div>}
        </div>
    )
}

const ConsoleEntry = (props: {entry: Entry, onAnswer: (requestId: string, optionId: string) => void}) => {
    const {entry, onAnswer} = props

    if (entry.kind === 'prompt') {
        return <div className='SessionConsole__entry SessionConsole__entry--prompt'>{entry.text}</div>
    }
    if (entry.kind === 'error') {
        return <div className='SessionConsole__entry SessionConsole__entry--failed'>{entry.text}</div>
    }
    if (entry.kind === 'text') {
        return (
            <div className={`SessionConsole__entry SessionConsole__entry--text${entry.thought ? ' is-thought' : ''}`}>
                {entry.text}
            </div>
        )
    }
    if (entry.kind === 'tool') {
        return (
            <div className='SessionConsole__entry SessionConsole__entry--tool'>
                <span className='SessionConsole__toolTitle'>{entry.title || entry.toolCallId}</span>
                {entry.status && <span className='SessionConsole__toolStatus'>{entry.status}</span>}
            </div>
        )
    }

    // Permission: still pending (buttons) or already decided (a record).
    return (
        <div className='SessionConsole__entry SessionConsole__entry--permission'>
            <div className='SessionConsole__permissionTitle'>{entry.title || entry.tool}</div>
            {entry.requestId && entry.options ?
                <div className='SessionConsole__permissionOptions'>
                    {entry.options.map((opt) => (
                        <Button
                            key={opt.optionId}
                            filled={opt.kind === 'allow_once'}
                            onClick={() => onAnswer(entry.requestId!, opt.optionId)}
                        >
                            {opt.name}
                        </Button>
                    ))}
                </div> :
                <span className='SessionConsole__permissionDecision'>{entry.decision}</span>}
        </div>
    )
}

export default React.memo(SessionConsole)
