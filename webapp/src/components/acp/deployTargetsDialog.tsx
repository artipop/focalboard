// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

// The Wails-generated Go bindings are PascalCase methods, not constructors.
/* eslint-disable new-cap */
import React, {useCallback, useEffect, useState} from 'react'
import {useIntl} from 'react-intl'

import Button from '../../widgets/buttons/button'
import Dialog from '../dialog'

import {agentBindings} from './agentReposDialog'

import './deployTargetsDialog.scss'

// One Dokku destination: a host, and nothing else that is not about reaching
// it. A card moved into the deploy column is published as <baseApp>-<branch
// slug>, served at that same name under the preview domain — the host itself,
// unless the two differ. baseApp is likewise left empty and derived from the
// repository name, which is what lets one target serve every repository
// pointed at it, and what makes everything a preview needs beyond that
// (environment, TLS, build time) a property of the repository, not of a host.
export type DeployTarget = {
    name: string
    sshHost: string
    sshUser?: string
    sshPort?: number
    sshKey?: string
    baseApp?: string
    baseDomain?: string
}

export function isDeployTargetsAvailable(): boolean {
    return Boolean(agentBindings()?.ListDeployTargets)
}

const emptyForm: DeployTarget = {name: '', sshHost: ''}

// previewHost spells out what the fields add up to, since the hostname is
// composed rather than typed: one label — the repository and the branch —
// under the preview domain, which is the Dokku host unless one is given.
function previewHost(t: DeployTarget): string {
    return `${t.baseApp || 'reponame'}-my-branch.${t.baseDomain || t.sshHost || 'dokku.example.com'}`
}

type Props = {
    onClose: () => void
}

const DeployTargetsDialog = (props: Props) => {
    const {onClose} = props
    const intl = useIntl()
    const bindings = agentBindings()

    const [targets, setTargets] = useState<DeployTarget[]>([])
    const [form, setForm] = useState<DeployTarget | null>(null)
    const [editingName, setEditingName] = useState<string | null>(null)
    const [error, setError] = useState('')

    const refresh = useCallback(async () => {
        if (!bindings?.ListDeployTargets) {
            return
        }
        try {
            setTargets(JSON.parse(await bindings.ListDeployTargets()) || [])
        } catch (e) {
            setError(String(e))
        }
    }, [bindings])

    useEffect(() => {
        refresh()
    }, [refresh])

    const startAdd = useCallback(() => {
        setForm({...emptyForm})
        setEditingName(null)
        setError('')
    }, [])

    const startEdit = useCallback((entry: DeployTarget) => {
        setForm({...entry})
        setEditingName(entry.name)
        setError('')
    }, [])

    const saveForm = useCallback(async () => {
        if (!bindings || !form) {
            return
        }
        setError('')
        const entry: DeployTarget = {...form, name: form.name.trim()}
        try {
            if (editingName) {
                await bindings.UpdateDeployTarget!(JSON.stringify(entry))
            } else {
                await bindings.AddDeployTarget!(JSON.stringify(entry))
            }
            setForm(null)
            await refresh()
        } catch (e) {
            setError(String(e))
        }
    }, [bindings, form, editingName, refresh])

    // Removing a target only forgets where to deploy: apps already running on
    // the Dokku host stay up until someone destroys them.
    const removeTarget = useCallback(async (name: string) => {
        if (!bindings?.RemoveDeployTarget) {
            return
        }
        setError('')
        try {
            await bindings.RemoveDeployTarget(name)
            await refresh()
        } catch (e) {
            setError(String(e))
        }
    }, [bindings, refresh])

    const updateForm = (patch: Partial<DeployTarget>) => setForm((f) => (f ? {...f, ...patch} : f))

    return (
        <Dialog
            className='DeployTargetsDialog'
            title={<span>{intl.formatMessage({id: 'DeployTargets.title', defaultMessage: 'Deploy targets'})}</span>}
            subtitle={<span>{intl.formatMessage({id: 'DeployTargets.subtitle', defaultMessage: 'Dokku hosts a card\'s branch is published to when it moves into the Deploy column. One branch becomes one app of its own, at “repository-branch.base-domain”.'})}</span>}
            onClose={onClose}
        >
            <div className='DeployTargetsDialog__content'>
                {targets.length === 0 && !form &&
                    <div className='DeployTargetsDialog__empty'>
                        {intl.formatMessage({id: 'DeployTargets.empty', defaultMessage: 'No deploy targets yet.'})}
                    </div>}

                {targets.map((entry) => (
                    <div
                        className='DeployTargetsDialog__row'
                        key={entry.name}
                    >
                        <span className='DeployTargetsDialog__name'>{entry.name}</span>
                        <span className='DeployTargetsDialog__where'>{`${entry.sshUser || 'dokku'}@${entry.sshHost} → *.${entry.baseDomain || entry.sshHost}`}</span>
                        <Button onClick={() => startEdit(entry)}>
                            {intl.formatMessage({id: 'DeployTargets.edit', defaultMessage: 'Edit'})}
                        </Button>
                        <Button onClick={() => removeTarget(entry.name)}>
                            {intl.formatMessage({id: 'DeployTargets.remove', defaultMessage: 'Remove'})}
                        </Button>
                    </div>
                ))}

                {form &&
                    <div className='DeployTargetsDialog__form'>
                        <label>
                            {intl.formatMessage({id: 'DeployTargets.name', defaultMessage: 'Name'})}
                            <input
                                value={form.name}
                                disabled={Boolean(editingName)}
                                placeholder={intl.formatMessage({id: 'DeployTargets.name-placeholder', defaultMessage: 'Name (also matched against the card\'s options)'})}
                                onChange={(e) => updateForm({name: e.target.value})}
                            />
                        </label>
                        <label>
                            {intl.formatMessage({id: 'DeployTargets.sshHost', defaultMessage: 'Dokku host'})}
                            <input
                                value={form.sshHost}
                                placeholder={'dokku.example.com'}
                                onChange={(e) => updateForm({sshHost: e.target.value})}
                            />
                        </label>
                        <label>
                            {intl.formatMessage({id: 'DeployTargets.sshUser', defaultMessage: 'SSH user (default dokku)'})}
                            <input
                                value={form.sshUser || ''}
                                placeholder={'dokku'}
                                onChange={(e) => updateForm({sshUser: e.target.value})}
                            />
                        </label>
                        <label>
                            {intl.formatMessage({id: 'DeployTargets.sshPort', defaultMessage: 'SSH port (default 22)'})}
                            <input
                                type='number'
                                value={form.sshPort || ''}
                                onChange={(e) => updateForm({sshPort: e.target.value ? Number(e.target.value) : undefined})}
                            />
                        </label>
                        <label>
                            {intl.formatMessage({id: 'DeployTargets.sshKey', defaultMessage: 'SSH key (absolute path, optional)'})}
                            <input
                                value={form.sshKey || ''}
                                placeholder={'/Users/me/.ssh/id_ed25519'}
                                onChange={(e) => updateForm({sshKey: e.target.value})}
                            />
                        </label>
                        <label>
                            {intl.formatMessage({id: 'DeployTargets.baseDomain', defaultMessage: 'Preview domain (optional) — the Dokku host itself by default'})}
                            <input
                                value={form.baseDomain || ''}
                                placeholder={form.sshHost || intl.formatMessage({id: 'DeployTargets.baseDomain-placeholder', defaultMessage: 'same as the Dokku host'})}
                                onChange={(e) => updateForm({baseDomain: e.target.value})}
                            />
                        </label>
                        <label>
                            {intl.formatMessage({id: 'DeployTargets.baseApp', defaultMessage: 'App name (optional) — the repository name by default'})}
                            <input
                                value={form.baseApp || ''}
                                placeholder={intl.formatMessage({id: 'DeployTargets.baseApp-placeholder', defaultMessage: 'repository name'})}
                                onChange={(e) => updateForm({baseApp: e.target.value})}
                            />
                        </label>
                        <div className='DeployTargetsDialog__hint'>
                            {intl.formatMessage(
                                {id: 'DeployTargets.hostname', defaultMessage: 'A branch is served at {host}'},
                                {host: previewHost(form)},
                            )}
                        </div>
                        <div className='DeployTargetsDialog__formActions'>
                            <Button
                                emphasis='primary'
                                onClick={saveForm}
                            >
                                {intl.formatMessage({id: 'DeployTargets.save', defaultMessage: 'Save'})}
                            </Button>
                            <Button onClick={() => setForm(null)}>
                                {intl.formatMessage({id: 'DeployTargets.cancel', defaultMessage: 'Cancel'})}
                            </Button>
                        </div>
                    </div>}

                {!form &&
                    <div className='DeployTargetsDialog__actions'>
                        <Button
                            emphasis='primary'
                            onClick={startAdd}
                        >
                            {intl.formatMessage({id: 'DeployTargets.add', defaultMessage: 'Add target…'})}
                        </Button>
                    </div>}

                {error &&
                    <div className='DeployTargetsDialog__error'>{error}</div>}
            </div>
        </Dialog>
    )
}

export default React.memo(DeployTargetsDialog)
