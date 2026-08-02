// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.
import {useEffect, useState} from 'react'
import type {Locale} from 'date-fns'

// react-day-picker 10 localises the calendar with a date-fns Locale object,
// where version 7 took a locale string plus moment's adapter. Loading them the
// same way useMomentLocale loads moment's: one dynamic import that Vite splits
// into its own chunk, which an English UI never fetches.
let locales: Record<string, Locale> | null = null
let pending: Promise<Record<string, Locale>> | null = null

function loadLocales(): Promise<Record<string, Locale>> {
    if (!pending) {
        pending = import('date-fns/locale').then((mod) => {
            locales = mod as unknown as Record<string, Locale>
            return locales
        })
    }
    return pending
}

// date-fns names locales enUS/ptBR where react-intl says en-us/pt-br.
function lookup(all: Record<string, Locale>, locale: string): Locale | undefined {
    const [language, region] = locale.toLowerCase().split(/[-_]/)
    if (region) {
        const regional = all[language + region.toUpperCase()]
        if (regional) {
            return regional
        }
    }
    return all[language]
}

// useDateFnsLocale returns the calendar locale for the given language, or
// undefined until it has loaded -- react-day-picker falls back to English then,
// which is what happened for the whole render before this resolved anyway.
export default function useDateFnsLocale(locale: string): Locale | undefined {
    const [resolved, setResolved] = useState<Locale | undefined>(() => (locales ? lookup(locales, locale) : undefined))

    useEffect(() => {
        if (!locale || locale === 'en') {
            setResolved(undefined)
            return undefined
        }
        if (locales) {
            setResolved(lookup(locales, locale))
            return undefined
        }

        let cancelled = false
        loadLocales().then((all) => {
            if (!cancelled) {
                setResolved(lookup(all, locale))
            }
        })
        return () => {
            cancelled = true
        }
    }, [locale])

    return resolved
}
