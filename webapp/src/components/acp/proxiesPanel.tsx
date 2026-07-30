// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

// The Wails-generated Go bindings are PascalCase methods, not constructors.
/* eslint-disable new-cap */
import React, {useCallback, useEffect, useState} from 'react'
import {useIntl} from 'react-intl'

import Button from '../../widgets/buttons/button'

import {agentBindings} from './agentReposDialog'

import './proxiesPanel.scss'

// A named network configuration. Agents reference one by name (their proxyName
// field) instead of carrying their own settings, so one proxy serves several
// agents and is edited in a single place.
export type ProxyEntry = {
    name: string
    proxy?: string
    noProxy?: string
    caCert?: string
    username?: string
    password?: string
}

// displayProxy hides any credentials the URL itself carries, so the list never
// renders a password (the dedicated fields are masked in the form).
function displayProxy(proxy?: string): string {
    if (!proxy) {
        return '—'
    }
    return proxy.replace(/\/\/[^/@]*@/, '//…@')
}

export function isProxiesAvailable(): boolean {
    return Boolean(agentBindings()?.ListProxies)
}

const emptyForm: ProxyEntry = {name: ''}

// onChange fires after the registry is edited, so the agent form's proxy list —
// the only place these entries are used — updates with it.
type Props = {
    onChange?: () => void
}

const ProxiesPanel = (props: Props) => {
    const {onChange} = props
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
            onChange?.()
        } catch (e) {
            setError(String(e))
        }
    }, [bindings, form, editingName, refresh, onChange])

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
            onChange?.()
        } catch (e) {
            setError(String(e))
        }
    }, [bindings, refresh, onChange])

    const updateForm = (patch: Partial<ProxyEntry>) => setForm((f) => (f ? {...f, ...patch} : f))

    return (
        <div className='ProxiesPanel'>
            <div className='ProxiesPanel__content'>
                <div className='ProxiesPanel__subtitle'>
                    {intl.formatMessage({id: 'Proxies.subtitle', defaultMessage: 'Named network configurations an agent picks from its "Proxy configuration" field above, so several agents can share one.'})}
                </div>

                {proxies.length === 0 && !form &&
                    <div className='ProxiesPanel__empty'>
                        {intl.formatMessage({id: 'Proxies.empty', defaultMessage: 'No proxy configurations yet.'})}
                    </div>}

                {proxies.map((entry) => (
                    <div
                        className='ProxiesPanel__row'
                        key={entry.name}
                    >
                        <span className='ProxiesPanel__name'>{entry.name}</span>
                        <span className='ProxiesPanel__proxy'>{displayProxy(entry.proxy)}</span>
                        <Button onClick={() => startEdit(entry)}>
                            {intl.formatMessage({id: 'Proxies.edit', defaultMessage: 'Edit'})}
                        </Button>
                        <Button onClick={() => removeProxy(entry.name)}>
                            {intl.formatMessage({id: 'Proxies.remove', defaultMessage: 'Remove'})}
                        </Button>
                    </div>
                ))}

                {form &&
                    <div className='ProxiesPanel__form'>
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
                            {intl.formatMessage({id: 'Proxies.username', defaultMessage: 'Username (optional)'})}
                            <input
                                value={form.username || ''}
                                autoComplete='off'
                                onChange={(e) => updateForm({username: e.target.value})}
                            />
                        </label>
                        <label>
                            {intl.formatMessage({id: 'Proxies.password', defaultMessage: 'Password (optional) — stored in the local config file'})}
                            <input
                                type='password'
                                value={form.password || ''}
                                autoComplete='new-password'
                                onChange={(e) => updateForm({password: e.target.value})}
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
                        <div className='ProxiesPanel__formActions'>
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
                    <div className='ProxiesPanel__actions'>
                        <Button
                            emphasis='primary'
                            onClick={startAdd}
                        >
                            {intl.formatMessage({id: 'Proxies.add', defaultMessage: 'Add configuration…'})}
                        </Button>
                    </div>}

                {error &&
                    <div className='ProxiesPanel__error'>{error}</div>}
            </div>
        </div>
    )
}

export default React.memo(ProxiesPanel)
