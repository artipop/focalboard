// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

// Shared plumbing for the two surfaces that watch an agent session: the console
// on a card and the planning dialog on a board. They differ only in what they
// are watching — a card, or one specific session — so the transcript model and
// the Wails event wiring live here.

// The Wails runtime methods are PascalCase, not constructors.
/* eslint-disable new-cap */
import React, {useEffect, useRef, useState} from 'react'

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

// One question the agent is asking, mirroring claude's AskUserQuestion input.
export type Question = {
    question: string
    header?: string
    multiSelect?: boolean
    options?: Array<{label: string, description?: string}>
}

export type PermissionOption = {
    optionId: string
    name: string
    kind: string
}

// One rendered line of the transcript.
export type Entry =
    | {kind: 'text', text: string, thought?: boolean}
    | {kind: 'prompt', text: string}
    | {kind: 'error', text: string}
    | {kind: 'tool', toolCallId: string, title?: string, status?: string}
    | {kind: 'permission', requestId?: string, tool?: string, title?: string, options?: PermissionOption[], decision?: string}
    | {kind: 'question', requestId?: string, questions: Question[], answer?: string}

export type SessionRecord = {
    id: string
    status: SessionStatus
    errorText?: string
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
        case 'question':
            entries = appendEntry(entries, {kind: 'question', requestId: p.requestId, questions: p.questions || []})
            break
        case 'answer':
            // Replay shows the question already answered, not still open.
            entries = entries.map((e) => (
                e.kind === 'question' && e.requestId === p.requestId ?
                    {...e, requestId: undefined, answer: p.text} : e))
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

export function useSessionStream(match: StreamMatch, onSession?: (payload: any) => void): SessionStream {
    const [entries, setEntries] = useState<Entry[]>([])
    const [session, setSession] = useState<SessionRecord | null>(null)
    const [error, setError] = useState('')

    const {cardId, sessionId} = match

    // Callers pass a fresh closure each render; a ref keeps the subscription
    // from being torn down and rebuilt every time, which would drop events.
    const onSessionRef = useRef(onSession)
    onSessionRef.current = onSession

    useEffect(() => {
        const runtime = (window as any).runtime
        if (!runtime?.EventsOn) {
            return undefined
        }
        if (!cardId && !sessionId) {
            return undefined
        }
        const mine = (payload: any) => Boolean(payload) &&
            (cardId ? payload.cardId === cardId : payload.sessionId === sessionId)

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
            runtime.EventsOn('acp:question', (payload: any) => {
                if (mine(payload)) {
                    setEntries((prev) => appendEntry(prev, {
                        kind: 'question',
                        requestId: payload.requestId,
                        questions: payload.questions || [],
                    }))
                }
            }),
            runtime.EventsOn('acp:answer', (payload: any) => {
                if (mine(payload)) {
                    setEntries((prev) => prev.map((e) => (
                        e.kind === 'question' && e.requestId === payload.requestId ?
                            {...e, requestId: undefined, answer: payload.text} : e)))
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
    }, [cardId, sessionId])

    return {entries, setEntries, session, setSession, error, setError}
}

type EntryProps = {
    entry: Entry
    onAnswer: (requestId: string, optionId: string) => void
    onAnswerQuestion?: (requestId: string, text: string) => void
}

export const ConsoleEntry = (props: EntryProps) => {
    const {entry, onAnswer} = props

    if (entry.kind === 'prompt') {
        return <div className='SessionConsole__entry SessionConsole__entry--prompt'>{entry.text}</div>
    }
    if (entry.kind === 'question') {
        return (
            <QuestionEntry
                entry={entry}
                onSubmit={props.onAnswerQuestion}
            />
        )
    }
    if (entry.kind === 'error') {
        return <div className='SessionConsole__entry SessionConsole__entry--failed'>{entry.text}</div>
    }
    if (entry.kind === 'text') {
        // Agents answer in markdown — lists, code fences, links — so it is
        // rendered the same way card comments are rather than shown as source.
        // Partial markdown mid-stream is fine: marked renders what has arrived.
        return (
            <div
                className={`SessionConsole__entry SessionConsole__entry--text${entry.thought ? ' is-thought' : ''}`}
                dangerouslySetInnerHTML={{__html: Utils.htmlFromMarkdown(entry.text)}}
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

// QuestionEntry renders the agent's own questions as a picker. Answers are
// composed into the text the model reads — the transport back to it is a single
// string, so the shape has to be readable rather than structured.
const QuestionEntry = (props: {
    entry: Extract<Entry, {kind: 'question'}>
    onSubmit?: (requestId: string, text: string) => void
}) => {
    const {entry, onSubmit} = props
    const [picked, setPicked] = useState<{[index: number]: string[]}>({})
    const [notes, setNotes] = useState<{[index: number]: string}>({})

    const toggle = (qi: number, label: string, multi: boolean) => {
        setPicked((prev) => {
            const current = prev[qi] || []
            if (!multi) {
                return {...prev, [qi]: current.includes(label) ? [] : [label]}
            }
            return {
                ...prev,
                [qi]: current.includes(label) ? current.filter((l) => l !== label) : [...current, label],
            }
        })
    }

    const answered = entry.questions.every((_, qi) => (picked[qi]?.length || notes[qi]?.trim()))

    const submit = () => {
        if (!entry.requestId || !onSubmit) {
            return
        }
        const lines = entry.questions.map((q, qi) => {
            const parts = [...(picked[qi] || [])]
            if (notes[qi]?.trim()) {
                parts.push(notes[qi].trim())
            }
            return `${qi + 1}. ${q.header || q.question}: ${parts.join('; ') || '—'}`
        })
        onSubmit(entry.requestId, `Ответы пользователя:\n${lines.join('\n')}`)
    }

    if (!entry.requestId) {
        return (
            <div className='SessionConsole__entry SessionConsole__entry--question is-answered'>
                {entry.questions.map((q, qi) => (
                    <div
                        key={qi}
                        className='SessionConsole__questionText'
                    >{q.question}</div>
                ))}
                {entry.answer && <div className='SessionConsole__questionAnswer'>{entry.answer}</div>}
            </div>
        )
    }

    return (
        <div className='SessionConsole__entry SessionConsole__entry--question'>
            {entry.questions.map((q, qi) => (
                <div
                    key={qi}
                    className='SessionConsole__question'
                >
                    <div className='SessionConsole__questionText'>{q.question}</div>
                    <div className='SessionConsole__questionOptions'>
                        {(q.options || []).map((opt) => (
                            <button
                                key={opt.label}
                                type='button'
                                className={`SessionConsole__option${(picked[qi] || []).includes(opt.label) ? ' is-picked' : ''}`}
                                onClick={() => toggle(qi, opt.label, Boolean(q.multiSelect))}
                            >
                                <span className='SessionConsole__optionLabel'>{opt.label}</span>
                                {opt.description && <span className='SessionConsole__optionDescription'>{opt.description}</span>}
                            </button>
                        ))}
                    </div>
                    <input
                        type='text'
                        className='SessionConsole__questionNote'
                        value={notes[qi] || ''}
                        placeholder='Свой ответ'
                        onChange={(e) => setNotes((prev) => ({...prev, [qi]: e.target.value}))}
                    />
                </div>
            ))}
            <Button
                filled={true}
                onClick={submit}
                disabled={!answered}
            >{'Ответить'}</Button>
        </div>
    )
}

// Transcript renders the whole conversation and follows the stream.
export const Transcript = (props: {
    entries: Entry[]
    onAnswer: (requestId: string, optionId: string) => void
    onAnswerQuestion?: (requestId: string, text: string) => void
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
                    onAnswerQuestion={props.onAnswerQuestion}
                />
            ))}
        </div>
    )
}
