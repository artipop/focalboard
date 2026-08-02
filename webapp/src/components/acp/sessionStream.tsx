// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

// Shared plumbing for the two surfaces that watch an agent session: the console
// on a card and the planning dialog on a board. They differ only in what they
// are watching — a card, or one specific session — so the transcript model and
// the Wails event wiring live here.

// The Wails runtime methods are PascalCase, not constructors.
/* eslint-disable new-cap */
import React, {useEffect, useRef, useState} from 'react'
import {useIntl} from 'react-intl'

import {Utils} from '../../utils'
import Button from '../../widgets/buttons/button'

import './sessionStream.scss'

// Session lifecycle, mirroring acp.SessionStatus. Only "done"/"failed"/
// "cancelled" are terminal; "idle" is a live session waiting for the next turn.
export type SessionStatus = 'queued' | 'running' | 'idle' | 'waiting_permission' | 'done' | 'failed' | 'cancelled'

const liveStatuses: SessionStatus[] = ['queued', 'running', 'idle', 'waiting_permission']

export function isLive(status?: SessionStatus): boolean {
    return Boolean(status && liveStatuses.includes(status))
}

export type PermissionOption = {
    optionId: string
    name: string
    kind: string
}

// One field of a form the agent asked for (ACP elicitation — Claude's own
// AskUserQuestion, or an MCP server's). The Go side flattens the schema, so
// there is nothing left here to interpret: draw what the field says it is.
export type ElicitationOption = {
    value: string
    title?: string
    description?: string
}

export type ElicitationField = {
    key: string
    title?: string
    description?: string
    type: 'select' | 'multiSelect' | 'text' | 'number' | 'boolean'
    options?: ElicitationOption[]
    required?: boolean

    // The free-text field that answers a select instead of choosing from it.
    customFor?: string
}

// One rendered line of the transcript.
export type Entry =
    | {kind: 'text', text: string, thought?: boolean}
    | {kind: 'prompt', text: string}
    | {kind: 'error', text: string}
    | {kind: 'tool', toolCallId: string, title?: string, status?: string}
    | {kind: 'permission', requestId?: string, tool?: string, title?: string, options?: PermissionOption[], decision?: string, byPolicy?: boolean}
    | {kind: 'elicitation', requestId?: string, message?: string, fields?: ElicitationField[], answered?: string, declined?: string}

export type SessionRecord = {
    id: string
    status: SessionStatus
    errorText?: string
    branch?: string
    worktreePath?: string
}

export type StoredEvent = {
    sessionId: string
    kind: string
    payload: any
}

// appendEntry merges consecutive text of the same kind into one paragraph, so a
// token-by-token stream does not become hundreds of DOM nodes.
export function appendEntry(entries: Entry[], next: Entry): Entry[] {
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
export function entriesFromStored(events: StoredEvent[]): Entry[] {
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
                byPolicy: p.byPolicy,
            })
            break
        case 'elicitation':
            // A form already answered (or declined) is replayed as what came of
            // it, the way a settled permission is.
            entries = appendEntry(entries, {
                kind: 'elicitation',
                requestId: p.pending ? p.requestId : undefined,
                message: p.message,
                fields: p.pending ? p.fields : undefined,
                answered: p.answered,
                declined: p.declined,
            })
            break
        default:
            break
        }
    }
    return entries
}

// Which session's events a surface cares about: a card's current one, or one
// specific session (planning has no card).
export type StreamMatch = {cardId?: string, sessionId?: string}

// useSessionStream subscribes to the Wails event bus and keeps the transcript
// and session state in step with the agent. It owns neither hydration nor
// attachment — the surfaces do that, since they differ.
export type SessionStream = {
    entries: Entry[]
    setEntries: React.Dispatch<React.SetStateAction<Entry[]>>
    session: SessionRecord | null
    setSession: React.Dispatch<React.SetStateAction<SessionRecord | null>>
    error: string
    setError: React.Dispatch<React.SetStateAction<string>>
}

export function useSessionStream(match: StreamMatch, onSession?: (payload: any) => void, onDeploy?: (payload: any) => void): SessionStream {
    const [entries, setEntries] = useState<Entry[]>([])
    const [session, setSession] = useState<SessionRecord | null>(null)
    const [error, setError] = useState('')

    const {cardId, sessionId} = match

    // Callers pass a fresh closure each render; a ref keeps the subscription
    // from being torn down and rebuilt every time, which would drop events.
    const onSessionRef = useRef(onSession)
    onSessionRef.current = onSession
    const onDeployRef = useRef(onDeploy)
    onDeployRef.current = onDeploy

    // A deploy started from the card shares its card id but not its console:
    // its events must not become the card's session or land in the transcript.
    const deployIds = useRef<Set<string>>(new Set())

    useEffect(() => {
        const runtime = (window as any).runtime
        if (!runtime?.EventsOn) {
            return undefined
        }
        if (!cardId && !sessionId) {
            return undefined
        }
        const mine = (payload: any) => Boolean(payload) &&
            (cardId ? payload.cardId === cardId : payload.sessionId === sessionId) &&
            !deployIds.current.has(payload.sessionId)

        const offs = [
            runtime.EventsOn('acp:session', (payload: any) => {
                if (payload?.deploy && cardId && payload.cardId === cardId) {
                    deployIds.current.add(payload.sessionId)
                    onDeployRef.current?.(payload)
                    return
                }
                if (!mine(payload)) {
                    return
                }
                setSession((prev) => ({
                    id: payload.sessionId,
                    status: payload.status,
                    errorText: payload.error || prev?.errorText,
                    branch: payload.branch || prev?.branch,
                    worktreePath: payload.worktreePath || prev?.worktreePath,
                }))
                if (payload.error) {
                    setError(payload.error)
                }
                onSessionRef.current?.(payload)
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
                        const pending = idx >= 0 ? prev[idx] : undefined
                        if (pending?.kind === 'permission') {
                            const next = [...prev]
                            next[idx] = {...pending, requestId: undefined, options: undefined, decision: payload.decision, byPolicy: payload.byPolicy}
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
                        byPolicy: payload.byPolicy,
                    })
                })
            }),
            runtime.EventsOn('acp:elicitation', (payload: any) => {
                if (!mine(payload)) {
                    return
                }
                setEntries((prev) => appendEntry(prev, {
                    kind: 'elicitation',
                    requestId: payload.pending ? payload.requestId : undefined,
                    message: payload.message,
                    fields: payload.pending ? payload.fields : undefined,
                    answered: payload.answered,
                    declined: payload.declined,
                }))
            }),
        ]
        return () => offs.forEach((off) => typeof off === 'function' && off())
    }, [cardId, sessionId])

    return {entries, setEntries, session, setSession, error, setError}
}

// MarkdownText re-parses only when its text changes. Streaming appends to the
// last entry, so without this every chunk re-parsed the whole transcript: for a
// 15 kB answer that is ~200 ms of parsing spread over the stream instead of
// ~2 ms, multiplied again by every earlier entry on screen.
const MarkdownText = React.memo((props: {text: string, thought?: boolean}) => {
    const html = React.useMemo(() => Utils.htmlFromMarkdown(props.text), [props.text])
    return (
        <div
            className={`SessionConsole__entry SessionConsole__entry--text${props.thought ? ' is-thought' : ''}`}
            dangerouslySetInnerHTML={{__html: html}}
        />
    )
})
MarkdownText.displayName = 'MarkdownText'

// ElicitationForm is the agent's own question, drawn from the fields the Go
// side flattened out of its schema. The answer goes back keyed by those same
// field names, so nothing here has to know which tool asked.
export const ElicitationFormEntry = (props: {
    requestId: string
    message?: string
    fields: ElicitationField[]
    onAnswer: (requestId: string, content: {[key: string]: unknown}) => void
}) => {
    const intl = useIntl()
    const [values, setValues] = useState<{[key: string]: unknown}>({})
    const [sent, setSent] = useState(false)

    const set = (key: string, value: unknown) => setValues((prev) => ({...prev, [key]: value}))
    const toggle = (key: string, value: string) => setValues((prev) => {
        const chosen = Array.isArray(prev[key]) ? (prev[key] as string[]) : []
        return {...prev, [key]: chosen.includes(value) ? chosen.filter((v) => v !== value) : [...chosen, value]}
    })

    // A free-text field belongs under the select it answers instead of.
    const custom = (key: string) => props.fields.filter((f) => f.customFor === key)
    const own = props.fields.filter((f) => !f.customFor)

    const submit = () => {
        const content: {[key: string]: unknown} = {}
        for (const [key, value] of Object.entries(values)) {
            if (value === undefined || value === '' || (Array.isArray(value) && value.length === 0)) {
                continue
            }
            content[key] = value
        }
        setSent(true)
        props.onAnswer(props.requestId, content)
    }

    const field = (f: ElicitationField) => {
        if (f.type === 'select' || f.type === 'multiSelect') {
            const chosen = values[f.key]
            return (f.options || []).map((option) => (
                <label
                    className='SessionConsole__formOption'
                    key={option.value}
                >
                    <input
                        type={f.type === 'select' ? 'radio' : 'checkbox'}
                        name={f.key}
                        checked={f.type === 'select' ? chosen === option.value : Array.isArray(chosen) && (chosen as string[]).includes(option.value)}
                        onChange={() => (f.type === 'select' ? set(f.key, option.value) : toggle(f.key, option.value))}
                    />
                    <span>
                        <span className='SessionConsole__formOptionTitle'>{option.title || option.value}</span>
                        {option.description && <span className='SessionConsole__formOptionHint'>{option.description}</span>}
                    </span>
                </label>
            ))
        }
        if (f.type === 'boolean') {
            return (
                <label className='SessionConsole__formOption'>
                    <input
                        type='checkbox'
                        checked={values[f.key] === true}
                        onChange={(e) => set(f.key, e.target.checked)}
                    />
                    <span>{f.title || f.key}</span>
                </label>
            )
        }
        return (
            <input
                className='SessionConsole__formInput'
                type={f.type === 'number' ? 'number' : 'text'}
                aria-label={f.title || f.key}
                placeholder={f.description || ''}
                value={(values[f.key] as string) || ''}
                onChange={(e) => set(f.key, f.type === 'number' ? Number(e.target.value) : e.target.value)}
            />
        )
    }

    return (
        <div className='SessionConsole__entry SessionConsole__entry--form'>
            {props.message && <div className='SessionConsole__formMessage'>{props.message}</div>}
            {own.map((f) => (
                <div
                    className='SessionConsole__formField'
                    key={f.key}
                >
                    {(f.title || f.description) &&
                        <div className='SessionConsole__formLabel'>{f.title || f.description}</div>}
                    {field(f)}
                    {custom(f.key).map((other) => (
                        <div key={other.key}>
                            <div className='SessionConsole__formLabel'>{other.title || other.key}</div>
                            {field({...other, customFor: undefined})}
                        </div>
                    ))}
                </div>
            ))}
            <Button
                emphasis='primary'
                disabled={sent}
                onClick={submit}
            >
                {intl.formatMessage({id: 'SessionConsole.form-answer', defaultMessage: 'Answer'})}
            </Button>
        </div>
    )
}

type EntryProps = {
    entry: Entry
    onAnswer: (requestId: string, optionId: string) => void
    onAnswerForm?: (requestId: string, content: {[key: string]: unknown}) => void
}

export const ConsoleEntry = React.memo((props: EntryProps) => {
    const {entry, onAnswer, onAnswerForm} = props
    const intl = useIntl()

    if (entry.kind === 'prompt') {
        return <div className='SessionConsole__entry SessionConsole__entry--prompt'>{entry.text}</div>
    }
    if (entry.kind === 'error') {
        return <div className='SessionConsole__entry SessionConsole__entry--failed'>{entry.text}</div>
    }
    if (entry.kind === 'text') {
        // Agents answer in markdown — lists, code fences, links — so it is
        // rendered the same way card comments are rather than shown as source.
        // Partial markdown mid-stream is fine: marked renders what has arrived.
        return (
            <MarkdownText
                text={entry.text}
                thought={entry.thought}
            />
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

    if (entry.kind === 'elicitation') {
        // A form still waiting is drawn; one already answered (or declined
        // because nobody was watching) is the record of what came of it.
        if (entry.requestId && entry.fields?.length && onAnswerForm) {
            return (
                <ElicitationFormEntry
                    requestId={entry.requestId}
                    message={entry.message}
                    fields={entry.fields}
                    onAnswer={onAnswerForm}
                />
            )
        }
        return (
            <div className='SessionConsole__entry SessionConsole__entry--form SessionConsole__entry--formSettled'>
                {entry.message && <div className='SessionConsole__formMessage'>{entry.message}</div>}
                <span className='SessionConsole__permissionDecision'>
                    {entry.answered || entry.declined ||
                        intl.formatMessage({id: 'SessionConsole.form-unanswerable', defaultMessage: 'The agent asked a question this console cannot show'})}
                </span>
            </div>
        )
    }

    // Permission: a question with buttons, or a record of one already settled.
    // The two must not look alike — a decision the policy made needs no answer,
    // and a box that looks like a prompt nobody can answer reads as broken.
    const pending = Boolean(entry.requestId && entry.options)
    return (
        <div className={`SessionConsole__entry SessionConsole__entry--permission${pending ? '' : ' SessionConsole__entry--permissionDecided'}`}>
            <div className='SessionConsole__permissionTitle'>{entry.title || entry.tool}</div>
            {pending ? <div className='SessionConsole__permissionOptions'>
                {entry.options!.map((opt) => (
                    <Button
                        key={opt.optionId}
                        filled={opt.kind === 'allow_once'}
                        onClick={() => onAnswer(entry.requestId!, opt.optionId)}
                    >
                        {opt.name}
                    </Button>
                ))}
            </div> : <span className='SessionConsole__permissionDecision'>
                {entry.byPolicy ? intl.formatMessage(
                    {id: 'SessionConsole.permission-by-policy', defaultMessage: '{decision} — automatically, by the tool policy'},
                    {decision: entry.decision},
                ) : entry.decision}
            </span>}
        </div>
    )
})
ConsoleEntry.displayName = 'ConsoleEntry'

// Transcript renders the whole conversation and follows the stream.
export const Transcript = (props: {
    entries: Entry[]
    onAnswer: (requestId: string, optionId: string) => void
    onAnswerForm?: (requestId: string, content: {[key: string]: unknown}) => void
}) => {
    const scrollRef = React.useRef<HTMLDivElement>(null)

    // Assigning scrollTop rather than calling scrollTo keeps this working under
    // jsdom, which implements only the property.
    useEffect(() => {
        const el = scrollRef.current
        if (el) {
            el.scrollTop = el.scrollHeight
        }
    }, [props.entries])

    return (
        <div
            ref={scrollRef}
            className='SessionConsole__transcript'
        >
            {props.entries.map((entry, i) => (
                <ConsoleEntry
                    key={i}
                    entry={entry}
                    onAnswer={props.onAnswer}
                    onAnswerForm={props.onAnswerForm}
                />
            ))}
        </div>
    )
}
