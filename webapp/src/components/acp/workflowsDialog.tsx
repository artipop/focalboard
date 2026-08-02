// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

// The Wails-generated Go bindings are PascalCase methods, not constructors.
/* eslint-disable new-cap */
import React, {useEffect, useState} from 'react'
import {useIntl} from 'react-intl'

import {Board, IPropertyTemplate} from '../../blocks/board'
import {Utils, IDType} from '../../utils'
import Button from '../../widgets/buttons/button'
import Dialog from '../dialog'

import {agentBindings} from './agentReposDialog'
import FlowDiagram, {StageCount} from './flowDiagram'

import './workflowsDialog.scss'

// A flow is the route a card takes across the board: stages (a column plus what
// runs there) joined by transitions. Both halves are edited here — the graph is
// the point, and a JSON blob would not be one.
export type FlowNode = {
    id: string
    column: string

    // The option the stage stands on, so renaming the column on the board
    // changes nothing. Empty in a route written before columns had ids.
    optionId?: string

    // Empty means "whatever the column does"; 'none' means the stage runs
    // nothing and waits for an event.
    action: string
    agentNames?: string[]
    deployName?: string

    // Where the stage was left on the canvas. Absent means "lay it out for me".
    x?: number
    y?: number
}

export type FlowEdge = {
    from: string
    to: string
    on: string
}

export type Flow = {
    name: string
    boardId?: string
    repoName?: string
    property?: string
    nodes: FlowNode[]
    edges: FlowEdge[]
}

// FlowTrigger is one transition kind the engine implements. The list comes from
// Go, so the editor can never offer a transition that does nothing.
// FlowOverview is where the board's cards stand on one route.
export type FlowOverview = {
    flow: string
    cards: number
    stages: StageCount[]
}

export type FlowTrigger = {
    kind: string
    source: string
    label: string
}

// The two outcome triggers are a stage's own outputs and are edited as such;
// everything else is an extra edge the stage waits for.
export const SUCCESS = 'success'
export const FAILURE = 'failure'

const ACTIONS = ['none', 'agent', 'deploy', 'test']

export function isWorkflowsAvailable(): boolean {
    return Boolean(agentBindings()?.ListFlows)
}

type Props = {
    board: Board
    onClose: () => void
}

// edgeTarget is the stage an event leads to, or '' when the graph says nothing.
export function edgeTarget(edges: FlowEdge[], from: string, on: string): string {
    return edges.find((e) => e.from === from && e.on === on)?.to || ''
}

// setEdge replaces the transition (from, on); an empty target removes it.
export function setEdge(edges: FlowEdge[], from: string, on: string, to: string): FlowEdge[] {
    const rest = edges.filter((e) => !(e.from === from && e.on === on))
    return to ? [...rest, {from, to, on}] : rest
}

// waitEdges are the stage's non-outcome transitions, in graph order.
export function waitEdges(edges: FlowEdge[], from: string): FlowEdge[] {
    return edges.filter((e) => e.from === from && e.on !== SUCCESS && e.on !== FAILURE)
}

const WorkflowsDialog = (props: Props) => {
    const {board, onClose} = props
    const intl = useIntl()
    const bindings = agentBindings()

    const [flows, setFlows] = useState<Flow[]>([])
    const [overview, setOverview] = useState<FlowOverview[]>([])
    const [templates, setTemplates] = useState<Flow[]>([])
    const [triggers, setTriggers] = useState<FlowTrigger[]>([])
    const [form, setForm] = useState<Flow | null>(null)
    const [editingName, setEditingName] = useState<string | null>(null)
    const [error, setError] = useState('')

    const selectProperties = board.cardProperties.filter((p: IPropertyTemplate) => p.type === 'select')

    // Columns are offered from the board itself: a stage pointing at a column
    // nobody has would only fail later, when a card cannot be moved into it.
    const columnOptions = (() => {
        const property = selectProperties.find((p) => p.name === form?.property) || selectProperties[0]
        return property?.options?.map((o) => o.value) || []
    })()

    const refresh = async () => {
        if (!bindings?.ListFlows) {
            return
        }
        try {
            setFlows(JSON.parse(await bindings.ListFlows(board.id)) || [])
            if (bindings.ListFlowTriggers) {
                setTriggers(JSON.parse(await bindings.ListFlowTriggers()) || [])
            }
            if (bindings.ListFlowTemplates) {
                setTemplates(JSON.parse(await bindings.ListFlowTemplates()) || [])
            }
            if (bindings.GetBoardFlowOverview) {
                setOverview(JSON.parse(await bindings.GetBoardFlowOverview(board.id)) || [])
            }
        } catch (e) {
            setError(String(e))
        }
    }

    useEffect(() => {
        refresh()
    }, [refresh])

    const startAdd = () => {
        setForm({
            name: '',
            property: selectProperties[0]?.name || '',
            nodes: [],
            edges: [],
        })
        setEditingName(null)
        setError('')
    }

    // A route the install ships with, opened in the editor rather than saved
    // behind the user's back: the graph is there to be looked at first.
    const startFromTemplate = (flow: Flow) => {
        setForm({...flow, nodes: [...(flow.nodes || [])], edges: [...(flow.edges || [])]})
        setEditingName(null)
        setError('')
    }

    const startEdit = (flow: Flow) => {
        setForm({...flow, nodes: [...(flow.nodes || [])], edges: [...(flow.edges || [])]})
        setEditingName(flow.name)
        setError('')
    }

    const saveForm = async () => {
        if (!bindings || !form) {
            return
        }
        setError('')
        try {
            const entry = {...form, name: form.name.trim(), boardId: form.boardId || board.id}
            if (editingName) {
                await bindings.UpdateFlow!(JSON.stringify(entry))
            } else {
                await bindings.AddFlow!(JSON.stringify(entry))
            }
            setForm(null)
            await refresh()
        } catch (e) {
            setError(String(e))
        }
    }

    const removeFlow = async (name: string) => {
        if (!bindings?.RemoveFlow) {
            return
        }
        setError('')
        try {
            await bindings.RemoveFlow(name)
            await refresh()
        } catch (e) {
            setError(String(e))
        }
    }

    const updateForm = (patch: Partial<Flow>) => setForm((f) => (f ? {...f, ...patch} : f))

    const updateNode = (id: string, patch: Partial<FlowNode>) => setForm((f) => (f ? {
        ...f,
        nodes: f.nodes.map((n) => (n.id === id ? {...n, ...patch} : n)),
    } : f))

    const addNode = () => setForm((f) => {
        if (!f) {
            return f
        }
        const used = new Set(f.nodes.map((n) => n.column))
        const free = columnOptions.find((c) => !used.has(c)) || ''
        return {...f, nodes: [...f.nodes, {id: Utils.createGuid(IDType.None), column: free, action: 'none'}]}
    })

    // Removing a stage takes its transitions with it, in and out: an edge to
    // nowhere is exactly what the engine refuses to save.
    const removeNode = (id: string) => setForm((f) => (f ? {
        ...f,
        nodes: f.nodes.filter((n) => n.id !== id),
        edges: f.edges.filter((e) => e.from !== id && e.to !== id),
    } : f))

    const changeEdge = (from: string, on: string, to: string) =>
        setForm((f) => (f ? {...f, edges: setEdge(f.edges, from, on, to)} : f))

    const replaceEdgeKind = (from: string, oldOn: string, newOn: string) => setForm((f) => {
        if (!f) {
            return f
        }
        const to = edgeTarget(f.edges, from, oldOn)
        return {...f, edges: setEdge(setEdge(f.edges, from, oldOn, ''), from, newOn, to)}
    })

    const waitTriggers = triggers.filter((t) => t.source !== 'outcome')

    // Only the shipped routes this registry does not already have: the point is
    // to fill a gap, not to offer a duplicate the engine would refuse.
    const missingTemplates = templates.filter(
        (t) => !flows.some((f) => f.name.toLowerCase() === t.name.toLowerCase()),
    )

    const nodeName = (id: string) => form?.nodes.find((n) => n.id === id)?.column || id

    const stageTargets = (exclude: string) => (form?.nodes || []).filter((n) => n.id !== exclude)

    return (
        <Dialog
            className='WorkflowsDialog'
            title={<span>{intl.formatMessage({id: 'Workflows.title', defaultMessage: 'Workflows'})}</span>}
            subtitle={<span>{intl.formatMessage({id: 'Workflows.subtitle', defaultMessage: 'The route a card takes across the board: what runs in each column, and which event moves the card on. A card without a matching flow keeps the standalone trigger columns.'})}</span>}
            onClose={onClose}
        >
            <div className='WorkflowsDialog__content'>
                {flows.length === 0 && !form &&
                    <div className='WorkflowsDialog__empty'>
                        {intl.formatMessage({id: 'Workflows.empty', defaultMessage: 'No flows yet.'})}
                    </div>}

                {!form && flows.map((flow) => {
                    const stages = overview.find((o) => o.flow === flow.name)
                    return (
                        <div
                            className='WorkflowsDialog__flow'
                            key={flow.name}
                        >
                            <div className='WorkflowsDialog__row'>
                                <span className='WorkflowsDialog__name'>{flow.name}</span>
                                <span className='WorkflowsDialog__route'>
                                    {stages && stages.cards > 0 ?
                                        intl.formatMessage({id: 'Workflows.cards-on-route', defaultMessage: '{count} cards on this route'}, {count: stages.cards}) :
                                        intl.formatMessage({id: 'Workflows.no-cards', defaultMessage: 'no cards on this route'})}
                                </span>
                                <Button onClick={() => startEdit(flow)}>
                                    {intl.formatMessage({id: 'Workflows.edit', defaultMessage: 'Edit'})}
                                </Button>
                                <Button onClick={() => removeFlow(flow.name)}>
                                    {intl.formatMessage({id: 'Workflows.remove', defaultMessage: 'Remove'})}
                                </Button>
                            </div>
                            <FlowDiagram
                                nodes={flow.nodes || []}
                                edges={flow.edges || []}
                                triggers={triggers}
                                counts={stages?.stages}
                                height={220}
                            />
                        </div>
                    )
                })}

                {form &&
                    <div className='WorkflowsDialog__form'>
                        <div className='WorkflowsDialog__formHeader'>
                            <label>
                                {intl.formatMessage({id: 'Workflows.name', defaultMessage: 'Name'})}
                                <input
                                    value={form.name}
                                    disabled={Boolean(editingName)}
                                    placeholder={intl.formatMessage({id: 'Workflows.name-placeholder', defaultMessage: 'Name (also matched against the card\'s options)'})}
                                    onChange={(e) => updateForm({name: e.target.value})}
                                />
                            </label>
                            <label>
                                {intl.formatMessage({id: 'Workflows.property', defaultMessage: 'Column property'})}
                                <select
                                    value={form.property || ''}
                                    onChange={(e) => updateForm({property: e.target.value})}
                                >
                                    {selectProperties.map((p) => (
                                        <option
                                            key={p.id}
                                            value={p.name}
                                        >{p.name}</option>
                                    ))}
                                </select>
                            </label>
                            <label>
                                {intl.formatMessage({id: 'Workflows.repoName', defaultMessage: 'Repository (optional)'})}
                                <input
                                    value={form.repoName || ''}
                                    placeholder={intl.formatMessage({id: 'Workflows.repoName-placeholder', defaultMessage: 'Registry name — cards of this repository use this flow'})}
                                    onChange={(e) => updateForm({repoName: e.target.value})}
                                />
                            </label>
                        </div>

                        <FlowDiagram
                            nodes={form.nodes}
                            edges={form.edges}
                            triggers={triggers}
                            onChange={(nodes, edges) => updateForm({nodes, edges})}
                        />
                        <div className='WorkflowsDialog__hint'>
                            {intl.formatMessage({id: 'Workflows.canvas-hint', defaultMessage: 'Drag a stage to move it; pull from its right side to join stages (upper point — on success, lower — on failure), from the bottom point to wait for an event. Delete removes what is selected.'})}
                        </div>

                        {form.nodes.map((node) => (
                            <div
                                className='WorkflowsDialog__node'
                                key={node.id}
                            >
                                <div className='WorkflowsDialog__nodeHead'>
                                    <select
                                        value={node.column}
                                        onChange={(e) => updateNode(node.id, {column: e.target.value})}
                                    >
                                        <option value=''>{intl.formatMessage({id: 'Workflows.pick-column', defaultMessage: '— pick a column —'})}</option>
                                        {columnOptions.map((c) => (
                                            <option
                                                key={c}
                                                value={c}
                                            >{c}</option>
                                        ))}
                                    </select>
                                    <select
                                        value={node.action}
                                        onChange={(e) => updateNode(node.id, {action: e.target.value})}
                                    >
                                        {ACTIONS.map((a) => (
                                            <option
                                                key={a}
                                                value={a}
                                            >{actionLabel(intl, a)}</option>
                                        ))}
                                    </select>
                                    <Button onClick={() => removeNode(node.id)}>
                                        {intl.formatMessage({id: 'Workflows.remove-stage', defaultMessage: 'Remove stage'})}
                                    </Button>
                                </div>

                                <div className='WorkflowsDialog__outcomes'>
                                    <label>
                                        {intl.formatMessage({id: 'Workflows.on-success', defaultMessage: '✅ on success →'})}
                                        <select
                                            value={edgeTarget(form.edges, node.id, SUCCESS)}
                                            onChange={(e) => changeEdge(node.id, SUCCESS, e.target.value)}
                                        >
                                            <option value=''>{intl.formatMessage({id: 'Workflows.stay', defaultMessage: '— stay here —'})}</option>
                                            {stageTargets(node.id).map((n) => (
                                                <option
                                                    key={n.id}
                                                    value={n.id}
                                                >{n.column}</option>
                                            ))}
                                        </select>
                                    </label>
                                    <label>
                                        {intl.formatMessage({id: 'Workflows.on-failure', defaultMessage: '❌ on failure →'})}
                                        <select
                                            value={edgeTarget(form.edges, node.id, FAILURE)}
                                            onChange={(e) => changeEdge(node.id, FAILURE, e.target.value)}
                                        >
                                            <option value=''>{intl.formatMessage({id: 'Workflows.stay', defaultMessage: '— stay here —'})}</option>
                                            {stageTargets(node.id).map((n) => (
                                                <option
                                                    key={n.id}
                                                    value={n.id}
                                                >{n.column}</option>
                                            ))}
                                        </select>
                                    </label>
                                </div>

                                {waitEdges(form.edges, node.id).map((edge) => (
                                    <div
                                        className='WorkflowsDialog__wait'
                                        key={edge.on}
                                    >
                                        <select
                                            value={edge.on}
                                            onChange={(e) => replaceEdgeKind(node.id, edge.on, e.target.value)}
                                        >
                                            {waitTriggers.map((t) => (
                                                <option
                                                    key={t.kind}
                                                    value={t.kind}
                                                >{t.label}</option>
                                            ))}
                                        </select>
                                        <span className='WorkflowsDialog__arrow'>{'→'}</span>
                                        <select
                                            value={edge.to}
                                            onChange={(e) => changeEdge(node.id, edge.on, e.target.value)}
                                        >
                                            {stageTargets(node.id).map((n) => (
                                                <option
                                                    key={n.id}
                                                    value={n.id}
                                                >{n.column}</option>
                                            ))}
                                        </select>
                                        <Button onClick={() => changeEdge(node.id, edge.on, '')}>
                                            {intl.formatMessage({id: 'Workflows.remove-wait', defaultMessage: 'Remove'})}
                                        </Button>
                                    </div>
                                ))}

                                <Button
                                    onClick={() => {
                                        const used = waitEdges(form.edges, node.id).map((e) => e.on)
                                        const free = waitTriggers.find((t) => !used.includes(t.kind))
                                        const target = stageTargets(node.id)[0]
                                        if (free && target) {
                                            changeEdge(node.id, free.kind, target.id)
                                        }
                                    }}
                                >
                                    {intl.formatMessage({id: 'Workflows.add-wait', defaultMessage: 'Wait for an event…'})}
                                </Button>
                            </div>
                        ))}

                        <div className='WorkflowsDialog__formActions'>
                            <Button onClick={addNode}>
                                {intl.formatMessage({id: 'Workflows.add-stage', defaultMessage: 'Add stage'})}
                            </Button>
                            <Button
                                emphasis='primary'
                                onClick={saveForm}
                            >
                                {intl.formatMessage({id: 'Workflows.save', defaultMessage: 'Save'})}
                            </Button>
                            <Button onClick={() => setForm(null)}>
                                {intl.formatMessage({id: 'Workflows.cancel', defaultMessage: 'Cancel'})}
                            </Button>
                        </div>
                        <div className='WorkflowsDialog__hint'>
                            {intl.formatMessage({id: 'Workflows.hint', defaultMessage: 'A stage runs its action when a card lands in its column — dragged by a person or moved by the flow itself. Where a stage has no transition for what happened, the card stays put and says so.'})}
                        </div>
                        {form.nodes.length > 0 && nodeName(form.nodes[0].id) === '' &&
                            <div className='WorkflowsDialog__hint'>
                                {intl.formatMessage({id: 'Workflows.pick-columns-hint', defaultMessage: 'Every stage needs a column.'})}
                            </div>}
                    </div>}

                {!form && missingTemplates.length > 0 &&
                    <div className='WorkflowsDialog__templates'>
                        <span className='WorkflowsDialog__hint'>
                            {intl.formatMessage({id: 'Workflows.templates', defaultMessage: 'Ready-made routes, the ones the board template names:'})}
                        </span>
                        {missingTemplates.map((flow) => (
                            <Button
                                key={flow.name}
                                onClick={() => startFromTemplate(flow)}
                            >{flow.name}</Button>
                        ))}
                    </div>}

                {!form &&
                    <div className='WorkflowsDialog__actions'>
                        <Button
                            emphasis='primary'
                            onClick={startAdd}
                        >
                            {intl.formatMessage({id: 'Workflows.add', defaultMessage: 'Add flow…'})}
                        </Button>
                    </div>}

                {error &&
                    <div className='WorkflowsDialog__error'>{error}</div>}
            </div>
        </Dialog>
    )
}

// actionLabel names what a stage does, in the reader's language.
function actionLabel(intl: {formatMessage: (d: {id: string, defaultMessage: string}) => string}, action: string): string {
    switch (action) {
    case 'agent':
        return intl.formatMessage({id: 'Workflows.action-agent', defaultMessage: 'agent works on the card'})
    case 'deploy':
        return intl.formatMessage({id: 'Workflows.action-deploy', defaultMessage: 'deploy the branch'})
    case 'test':
        return intl.formatMessage({id: 'Workflows.action-test', defaultMessage: 'test the preview'})
    default:
        return intl.formatMessage({id: 'Workflows.action-none', defaultMessage: 'nothing — wait for an event'})
    }
}

export default React.memo(WorkflowsDialog)
