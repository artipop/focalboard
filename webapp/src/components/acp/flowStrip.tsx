// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

// The Wails-generated Go bindings are PascalCase methods, not constructors.
/* eslint-disable new-cap */
import React, {useCallback, useEffect, useState} from 'react'
import {useIntl} from 'react-intl'

import {agentBindings} from './agentReposDialog'

import './flowStrip.scss'

// Where the card is on its route, on the card itself. Until now this could only
// be reconstructed by reading the comments backwards, which is a poor way to
// answer the one question a stalled card raises: what is it waiting for?

export type CardFlowStage = {
    nodeId: string
    column: string
    action: string
    crew?: string[]
    current: boolean
    done: boolean
}

export type CardFlow = {
    flow: string
    stages: CardFlowStage[]
    currentNodeId: string
    since?: string
    branch?: string
    waitingFor?: string[]
    queued?: boolean
    running?: boolean
}

export function isFlowStripAvailable(): boolean {
    return Boolean(agentBindings()?.GetCardFlow)
}

// waited says how long the card has stood where it stands, in the units a
// person would use out loud. The number and the unit are kept apart so the
// wording stays translatable.
export function waited(since: string | undefined, now: number): {value: number, unit: string} | null {
    if (!since) {
        return null
    }
    const started = Date.parse(since)
    if (isNaN(started)) {
        return null
    }
    const minutes = Math.max(0, Math.floor((now - started) / 60000))
    if (minutes < 60) {
        return {value: minutes, unit: 'minutes'}
    }
    const hours = Math.floor(minutes / 60)
    if (hours < 24) {
        return {value: hours, unit: 'hours'}
    }
    return {value: Math.floor(hours / 24), unit: 'days'}
}

type Props = {
    cardId: string
}

const FlowStrip = (props: Props) => {
    const {cardId} = props
    const intl = useIntl()
    const bindings = agentBindings()
    const [flow, setFlow] = useState<CardFlow | null>(null)

    const refresh = useCallback(async () => {
        if (!bindings?.GetCardFlow) {
            return
        }
        try {
            setFlow(JSON.parse(await bindings.GetCardFlow(cardId)))
        } catch (e) {
            setFlow(null)
        }
    }, [bindings, cardId])

    useEffect(() => {
        refresh()

        // A session starting, finishing or moving the card changes the answer.
        const runtime = (window as any).runtime
        const off = runtime?.EventsOn?.('acp:session', () => refresh())
        return () => off?.()
    }, [refresh])

    if (!flow || flow.stages.length === 0) {
        return null
    }

    const age = waited(flow.since, Date.now())
    const since = age === null ? '' : sinceLabel(intl as Parameters<typeof sinceLabel>[0], age)
    const status = () => {
        if (flow.running) {
            return intl.formatMessage({id: 'FlowStrip.running', defaultMessage: 'working now'})
        }
        if (flow.queued) {
            return intl.formatMessage({id: 'FlowStrip.queued', defaultMessage: 'waiting for a free place in the column'})
        }
        if (flow.waitingFor && flow.waitingFor.length > 0) {
            return intl.formatMessage({id: 'FlowStrip.waiting', defaultMessage: 'waiting: {events}'}, {events: flow.waitingFor.join(', ')})
        }
        return intl.formatMessage({id: 'FlowStrip.idle', defaultMessage: 'nothing to wait for here — the card moves on by hand'})
    }

    return (
        <div className='FlowStrip'>
            <div className='FlowStrip__head'>
                <span className='FlowStrip__name'>{flow.flow}</span>
                {flow.branch &&
                    <span className='FlowStrip__branch'>{flow.branch}</span>}
            </div>
            <div className='FlowStrip__stages'>
                {flow.stages.map((stage) => (
                    <span
                        key={stage.nodeId}
                        className={`FlowStrip__stage${stage.current ? ' FlowStrip__stage--current' : ''}${stage.done ? ' FlowStrip__stage--done' : ''}`}
                        title={stage.crew && stage.crew.length > 0 ? stage.crew.join(', ') : ''}
                    >{stage.column}</span>
                ))}
            </div>
            <div className='FlowStrip__status'>
                {status()}
                {since && ` · ${since}`}
            </div>
        </div>
    )
}

// sinceLabel is "for 20 minutes" in the reader's language.
function sinceLabel(
    intl: {formatMessage: (d: {id: string, defaultMessage: string}, v?: Record<string, unknown>) => string},
    age: {value: number, unit: string},
): string {
    switch (age.unit) {
    case 'hours':
        return intl.formatMessage({id: 'FlowStrip.hours', defaultMessage: '{value} h'}, {value: age.value})
    case 'days':
        return intl.formatMessage({id: 'FlowStrip.days', defaultMessage: '{value} d'}, {value: age.value})
    default:
        return intl.formatMessage({id: 'FlowStrip.minutes', defaultMessage: '{value} min'}, {value: age.value})
    }
}

export default React.memo(FlowStrip)
