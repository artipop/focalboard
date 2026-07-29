// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

// The Wails-generated Go bindings are PascalCase methods, not constructors.
/* eslint-disable new-cap */
import React, {useCallback, useEffect, useRef, useState} from 'react'
import {useIntl} from 'react-intl'

import Button from '../../widgets/buttons/button'

import {agentBindings} from './agentReposDialog'
import {
    SessionRecord,
    StoredEvent,
    Transcript,
    entriesFromStored,
    isLive,
    useSessionStream,
} from './sessionStream'

import './sessionConsole.scss'

export function isSessionConsoleAvailable(): boolean {
    return Boolean(agentBindings()?.GetCardSessions)
}

type Props = {
    cardId: string
}

const SessionConsole = (props: Props) => {
    const {cardId} = props
    const intl = useIntl()
    const bindings = agentBindings()

    const [draft, setDraft] = useState('')
    const [busy, setBusy] = useState(false)

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

    // A session that starts while the card is already open is only ever
    // announced through the event bus, so this is where it gets attached.
    const {entries, setEntries, session, setSession, error, setError} = useSessionStream(
        {cardId},
        useCallback((payload: any) => {
            attachTo(isLive(payload.status) ? payload.sessionId : null)
        }, [attachTo]),
    )

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
    }, [attachTo, bindings, cardId, setEntries, setError, setSession])

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
    }, [bindings, cardId, setEntries, setError, setSession])

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
    }, [bindings, draft, session, setError])

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
    }, [bindings, session, setError])

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
    }, [bindings, session, setError])

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
                <Transcript
                    entries={entries}
                    onAnswer={answer}
                />}

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

export default React.memo(SessionConsole)
