// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

// The list behind every select in the app, with no framework and no DOM in it:
// which rows are drawn, which of them a keystroke moves to, and what a key
// means at all. `widgets/combobox.tsx` is the React view over it.
//
// This is what replaces react-select, whose own answer to all of this was
// spread across a component tree and an emotion style object.

export type ComboboxOption<T> = {

    // What `getOptionValue` used to answer: stable, and what selection compares.
    id: string

    // What `getOptionLabel` used to answer: what typing is matched against.
    label: string

    data: T
}

export type ComboboxGroup<T> = {
    label: string
    options: Array<ComboboxOption<T>>
}

export type ComboboxItem<T> = ComboboxOption<T> | ComboboxGroup<T>

// A row is drawn; only an option row can be highlighted, which is the whole
// reason the list is flattened rather than walked as a tree.
export type ComboboxRow<T> =
    | {kind: 'group', key: string, label: string}
    | {kind: 'option', key: string, option: ComboboxOption<T>}

function isGroup<T>(item: ComboboxItem<T>): item is ComboboxGroup<T> {
    return Array.isArray((item as ComboboxGroup<T>).options)
}

export function toRows<T>(items: Array<ComboboxItem<T>>): Array<ComboboxRow<T>> {
    const rows: Array<ComboboxRow<T>> = []

    for (const item of items) {
        if (isGroup(item)) {
            rows.push({kind: 'group', key: `group:${item.label}`, label: item.label})
            for (const option of item.options) {
                rows.push({kind: 'option', key: option.id, option})
            }
        } else {
            rows.push({kind: 'option', key: item.id, option: item})
        }
    }

    return rows
}

export function defaultMatches(label: string, query: string): boolean {
    return label.toLowerCase().includes(query.trim().toLowerCase())
}

// Filters the options and drops any group left with nothing under it, so a
// heading never stands alone over an empty stretch of menu.
export function filterRows<T>(
    rows: Array<ComboboxRow<T>>,
    query: string,
    matches: (option: ComboboxOption<T>, query: string) => boolean = (option, q) => defaultMatches(option.label, q),
): Array<ComboboxRow<T>> {
    if (!query) {
        return rows
    }

    const kept: Array<ComboboxRow<T>> = []
    for (const row of rows) {
        if (row.kind === 'group') {
            kept.push(row)
            continue
        }
        if (matches(row.option, query)) {
            kept.push(row)
        }
    }

    return kept.filter((row, index) => {
        if (row.kind !== 'group') {
            return true
        }
        const next = kept[index + 1]
        return next !== undefined && next.kind === 'option'
    })
}

export function optionAt<T>(rows: Array<ComboboxRow<T>>, index: number): ComboboxOption<T> | undefined {
    const row = rows[index]
    return row && row.kind === 'option' ? row.option : undefined
}

export function firstOption<T>(rows: Array<ComboboxRow<T>>): number {
    return rows.findIndex((row) => row.kind === 'option')
}

// The index of the next highlightable option in `delta`'s direction, skipping
// group headings and wrapping at both ends. -1 when there is nothing to move to.
export function nextOption<T>(rows: Array<ComboboxRow<T>>, from: number, delta: number): number {
    const count = rows.length
    if (count === 0 || !rows.some((row) => row.kind === 'option')) {
        return -1
    }

    let cursor = from
    for (let step = 0; step < count; step++) {
        cursor = (((cursor + delta) % count) + count) % count
        if (rows[cursor].kind === 'option') {
            return cursor
        }
    }

    return -1
}

// What a keystroke means to a combobox, so the view holds state and nothing
// else. `hasQuery` is what tells Backspace from a deletion of the last value.
export type ComboboxIntent = 'open' | 'close' | 'previous' | 'next' | 'choose' | 'removeLast' | 'none'

export function keyIntent(key: string, isOpen: boolean, hasQuery: boolean): ComboboxIntent {
    switch (key) {
    case 'ArrowDown':
        return isOpen ? 'next' : 'open'
    case 'ArrowUp':
        return isOpen ? 'previous' : 'open'
    case 'Enter':
    case 'Tab':
        return isOpen ? 'choose' : 'none'
    case 'Escape':
        return isOpen ? 'close' : 'none'
    case 'Backspace':
        return hasQuery ? 'none' : 'removeLast'
    default:
        return 'none'
    }
}
