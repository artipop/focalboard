// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.
import {useEffect, useRef} from 'react'

import {bindHotkey, type HotkeyHandler} from '../hotkeys'

// The React half of `hotkeys.ts`, and all of it: the handler is kept in a ref so
// it always sees the current render, which is why call sites pass no dependency
// list — react-hotkeys-hook needed one because it rebound the shortcut instead,
// and a handler given no dependencies quietly went on seeing the first render.
export function useHotkeys(shortcut: string, handler: HotkeyHandler): void {
    const latest = useRef(handler)

    useEffect(() => {
        latest.current = handler
    })

    useEffect(() => bindHotkey(shortcut, (event) => latest.current(event)), [shortcut])
}
