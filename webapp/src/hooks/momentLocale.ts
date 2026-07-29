// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.
import {useEffect, useState} from 'react'

// moment registers a locale's definitions as a side effect of importing it. The
// webpack build could resolve require(`moment/locale/${locale}`) at run time,
// but an ESM bundle has no require, so the locales are pulled in through a
// static dynamic import instead. Vite splits it into its own chunk, which an
// English UI never fetches.
let loaded = false
let pending: Promise<unknown> | null = null

function loadLocales(): Promise<unknown> {
    if (!pending) {
        pending = import('moment/min/locales').then((mod) => {
            loaded = true
            return mod
        })
    }
    return pending
}

// useMomentLocale makes moment speak the given locale, re-rendering once the
// definitions arrive so dates stop showing in the English fallback.
export default function useMomentLocale(locale: string): void {
    const [, setRevision] = useState(0)

    useEffect(() => {
        if (!locale || locale === 'en' || loaded) {
            return undefined
        }
        let cancelled = false
        loadLocales().then(() => {
            if (!cancelled) {
                setRevision((revision) => revision + 1)
            }
        })
        return () => {
            cancelled = true
        }
    }, [locale])
}
