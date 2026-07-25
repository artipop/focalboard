// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

// The Wails-generated Go bindings are PascalCase methods, not constructors.
/* eslint-disable new-cap */
import React, {useCallback, useEffect, useState} from 'react'
import {useIntl} from 'react-intl'

import Button from '../../widgets/buttons/button'
import Dialog from '../dialog'

import {agentBindings} from './agentReposDialog'

import './proxiesDialog.scss'

// A named network configuration. Agents reference one by name (their proxyName
// field) instead of carrying their own settings, so one proxy serves several
// agents and is edited in a single place.
export type ProxyEntry = {
    name: string
    proxy?: string
    noProxy?: string
    caCert?: string
}

export function isProxiesAvailable(): boolean {
    return Boolean(agentBindings()?.ListProxies)
}

const emptyForm: ProxyEntry = {name: ''}

type Props = {
    onClose: () => void
}

const ProxiesDialog = (props: Props) => {
    const {onClose} = props
    const intl = useIntl()
    const bindings = agentBindings()

    const [proxies, setProxies] = useState<ProxyEntry[]>([])
    const [form, setForm] = useState<ProxyEntry | null>(null)
    const [editingName, setEditingName] = useState<string | null>(null)
    const [error, setError] = useState('')

    const refresh = useCallback(async () => {
        if (!bindings?.ListProxies) {
            return
        }
        try {
            setProxies(JSON.parse(await bindings.ListProxies()) || [])
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

    const startEdit = useCallback((entry: ProxyEntry) => {
        setForm({...entry})
        setEditingName(entry.name)
        setError('')
    }, [])

    const saveForm = useCallback(async () => {
        if (!bindings || !form) {
            return
        }
        setError('')
        const entry: ProxyEntry = {...form, name: form.name.trim()}
        try {
            if (editingName) {
                await bindings.UpdateProxy!(JSON.stringify(entry))
            } else {
                await bindings.AddProxy!(JSON.stringify(entry))
            }
            setForm(null)
            await refresh()
        } catch (e) {
            setError(String(e))
        }
    }, [bindings, form, editingName, refresh])

    // Removal is refused by the backend while agents still reference the entry,
    // so the error surfaces which agents to switch over first.
    const removeProxy = useCallback(async (name: string) => {
        if (!bindings?.RemoveProxy) {
            return
        }
        setError('')
        try {
            await bindings.RemoveProxy(name)
            await refresh()
        } catch (e) {
            setError(String(e))
        }
    }, [bindings, refresh])

    const updateForm = (patch: Partial<ProxyEntry>) => setForm((f) => (f ? {...f, ...patch} : f))

    return (
        <Dialog
            className='ProxiesDialog'
            title={<span>{intl.formatMessage({id: 'Proxies.title', defaultMessage: 'Proxy configurations'})}</span>}
            subtitle={<span>{intl.formatMessage({id: 'Proxies.subtitle', defaultMessage: 'Named network configurations. Each agent picks one in the Agents dialog, so several agents can share a proxy.'})}</span>}
            onClose={onClose}
        >
            <div className='ProxiesDialog__content'>
                {proxies.length === 0 && !form &&
                    <div className='ProxiesDialog__empty'>
                        {intl.formatMessage({id: 'Proxies.empty', defaultMessage: 'No proxy configurations yet.'})}
                    </div>}

                {proxies.map((entry) => (
                    <div
                        className='ProxiesDialog__row'
                        key={entry.name}
                    >
                        <span className='ProxiesDialog__name'>{entry.name}</span>
                        <span className='ProxiesDialog__proxy'>{entry.proxy || '—'}</span>
                        <Button onClick={() => startEdit(entry)}>
                            {intl.formatMessage({id: 'Proxies.edit', defaultMessage: 'Edit'})}
                        </Button>
                        <Button onClick={() => removeProxy(entry.name)}>
                            {intl.formatMessage({id: 'Proxies.remove', defaultMessage: 'Remove'})}
                        </Button>
                    </div>
                ))}

                {form &&
                    <div className='ProxiesDialog__form'>
                        <label>
                            {intl.formatMessage({id: 'Proxies.name', defaultMessage: 'Name'})}
                            <input
                                value={form.name}
                                disabled={Boolean(editingName)}
                                placeholder={intl.formatMessage({id: 'Proxies.name-placeholder', defaultMessage: 'Name (shown in the agent\'s proxy list)'})}
                                onChange={(e) => updateForm({name: e.target.value})}
                            />
                        </label>
                        <label>
                            {intl.formatMessage({id: 'Proxies.proxy', defaultMessage: 'Proxy URL — HTTP(S)_PROXY / ALL_PROXY'})}
                            <input
                                value={form.proxy || ''}
                                placeholder={'http://proxy.example.com:8080'}
                                onChange={(e) => updateForm({proxy: e.target.value})}
                            />
                        </label>
                        <label>
                            {intl.formatMessage({id: 'Proxies.noProxy', defaultMessage: 'Bypass proxy for (comma-separated)'})}
                            <input
                                value={form.noProxy || ''}
                                placeholder={'localhost,127.0.0.1,.internal'}
                                onChange={(e) => updateForm({noProxy: e.target.value})}
                            />
                        </label>
                        <label>
                            {intl.formatMessage({id: 'Proxies.caCert', defaultMessage: 'CA bundle — PEM for a TLS-inspecting proxy'})}
                            <input
                                value={form.caCert || ''}
                                placeholder={'/etc/ssl/my-ca.pem'}
                                onChange={(e) => updateForm({caCert: e.target.value})}
                            />
                        </label>
                        <div className='ProxiesDialog__formActions'>
                            <Button
                                emphasis='primary'
                                onClick={saveForm}
                            >
                                {intl.formatMessage({id: 'Proxies.save', defaultMessage: 'Save'})}
                            </Button>
                            <Button onClick={() => setForm(null)}>
                                {intl.formatMessage({id: 'Proxies.cancel', defaultMessage: 'Cancel'})}
                            </Button>
                        </div>
                    </div>}

                {!form &&
                    <div className='ProxiesDialog__actions'>
                        <Button
                            emphasis='primary'
                            onClick={startAdd}
                        >
                            {intl.formatMessage({id: 'Proxies.add', defaultMessage: 'Add configuration…'})}
                        </Button>
                    </div>}

                {error &&
                    <div className='ProxiesDialog__error'>{error}</div>}
            </div>
        </Dialog>
    )
}

export default React.memo(ProxiesDialog)
