// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

// Keyboard shortcuts with no framework in them. `hooks/hotkeys.ts` is the React
// binding, and it is the only part a port to another framework would rewrite.
//
// This replaces react-hotkeys-hook 3 and hotkeys-js under it, keeping the two
// behaviours the call sites were relying on:
//
//   - modifiers match exactly, so `ctrl+z` stays silent while shift is held and
//     `shift+ctrl+z` is the only binding that fires;
//   - a shortcut never fires while the user is typing, so Esc in a dialog's text
//     field belongs to the field.

type Modifiers = {
    shift: boolean
    alt: boolean
    ctrl: boolean
    meta: boolean
}

// One `ctrl+shift+f`. Named keys are compared against `KeyboardEvent.key`, which
// does not move with the keyboard layout; everything else against `.code`, so
// `ctrl+d` is the same physical key under a Cyrillic layout as under a Latin one.
// That is what matching hotkeys-js's `keyCode` used to give us for free.
type Chord = Modifiers & {
    code?: string
    key?: string
}

const modifierAliases: Record<string, keyof Modifiers> = {
    shift: 'shift',
    alt: 'alt',
    option: 'alt',
    ctrl: 'ctrl',
    control: 'ctrl',
    cmd: 'meta',
    command: 'meta',
    meta: 'meta',
}

const namedKeys: Record<string, string> = {
    esc: 'Escape',
    escape: 'Escape',
    del: 'Delete',
    delete: 'Delete',
    backspace: 'Backspace',
    enter: 'Enter',
    return: 'Enter',
    tab: 'Tab',
    space: ' ',
    up: 'ArrowUp',
    down: 'ArrowDown',
    left: 'ArrowLeft',
    right: 'ArrowRight',
    home: 'Home',
    end: 'End',
}

function parseChord(text: string): Chord | undefined {
    const chord: Chord = {shift: false, alt: false, ctrl: false, meta: false}
    let name = ''

    for (const part of text.split('+').map((p) => p.trim().toLowerCase())) {
        const modifier = modifierAliases[part]
        if (modifier) {
            chord[modifier] = true
        } else if (part) {
            name = part
        }
    }

    if (!name) {
        return undefined
    }

    if (namedKeys[name]) {
        chord.key = namedKeys[name]
    } else if (name.length === 1 && name >= 'a' && name <= 'z') {
        chord.code = `Key${name.toUpperCase()}`
    } else if (name.length === 1 && name >= '0' && name <= '9') {
        chord.code = `Digit${name}`
    } else {
        chord.key = name
    }

    return chord
}

// `'ctrl+z,cmd+z'` — a comma separates alternatives, any one of which fires.
export function parseHotkey(shortcut: string): Chord[] {
    return shortcut.
        split(',').
        map(parseChord).
        filter((chord): chord is Chord => chord !== undefined)
}

function matchesChord(event: KeyboardEvent, chord: Chord): boolean {
    if (event.shiftKey !== chord.shift || event.altKey !== chord.alt ||
        event.ctrlKey !== chord.ctrl || event.metaKey !== chord.meta) {
        return false
    }

    if (chord.code) {
        return event.code === chord.code
    }

    return event.key.toLowerCase() === chord.key?.toLowerCase()
}

export function matchesHotkey(event: KeyboardEvent, shortcut: string): boolean {
    return parseHotkey(shortcut).some((chord) => matchesChord(event, chord))
}

// Where the shortcut is the field's, not ours.
export function isTypingTarget(target: EventTarget | null): boolean {
    const element = target as HTMLElement | null
    if (!element) {
        return false
    }

    return element.isContentEditable ||
        element.tagName === 'INPUT' ||
        element.tagName === 'TEXTAREA' ||
        element.tagName === 'SELECT'
}

export type HotkeyHandler = (event: KeyboardEvent) => void

// Returns the unbind.
export function bindHotkey(shortcut: string, handler: HotkeyHandler, target: EventTarget = document): () => void {
    const chords = parseHotkey(shortcut)
    if (chords.length === 0) {
        return () => {}
    }

    const listener = (event: Event) => {
        const keyboardEvent = event as KeyboardEvent
        if (isTypingTarget(keyboardEvent.target)) {
            return
        }
        if (chords.some((chord) => matchesChord(keyboardEvent, chord))) {
            handler(keyboardEvent)
        }
    }

    target.addEventListener('keydown', listener)
    return () => target.removeEventListener('keydown', listener)
}
