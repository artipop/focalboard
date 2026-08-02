// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.
import {defaultMatches, filterRows, firstOption, keyIntent, nextOption, optionAt, toRows, type ComboboxOption} from './combobox'

const option = (id: string, label = id): ComboboxOption<string> => ({id, label, data: id})

const flat = [option('one'), option('two'), option('three')]

const grouped = [
    {label: 'Board members', options: [option('ann'), option('bob')]},
    {label: 'Not board members', options: [option('cyd')]},
]

describe('combobox', () => {
    describe('toRows', () => {
        it('leaves a flat list flat', () => {
            expect(toRows(flat).map((row) => row.kind)).toEqual(['option', 'option', 'option'])
        })

        it('puts a heading above each group', () => {
            expect(toRows(grouped).map((row) => row.kind)).
                toEqual(['group', 'option', 'option', 'group', 'option'])
        })
    })

    describe('filterRows', () => {
        const rows = toRows(flat)

        it('keeps everything without a query', () => {
            expect(filterRows(rows, '')).toHaveLength(3)
        })

        it('matches anywhere in the label, ignoring case and padding', () => {
            expect(filterRows(rows, 'O').map((row) => row.key)).toEqual(['one', 'two'])
            expect(filterRows(rows, '  ONE  ').map((row) => row.key)).toEqual(['one'])
        })

        it('takes a matcher of its own', () => {
            const startsWith = (o: ComboboxOption<string>, q: string) => o.label.startsWith(q)
            expect(filterRows(rows, 'o', startsWith).map((row) => row.key)).toEqual(['one'])
        })

        // A heading over nothing is the thing that looks broken.
        it('drops a group left with no options', () => {
            const kept = filterRows(toRows(grouped), 'cyd')

            expect(kept.map((row) => row.key)).toEqual(['group:Not board members', 'cyd'])
        })

        it('drops every group when nothing matches', () => {
            expect(filterRows(toRows(grouped), 'nobody')).toEqual([])
        })
    })

    describe('nextOption', () => {
        it('steps over the options of a flat list', () => {
            const rows = toRows(flat)

            expect(nextOption(rows, 0, 1)).toBe(1)
            expect(nextOption(rows, 1, -1)).toBe(0)
        })

        it('wraps at both ends', () => {
            const rows = toRows(flat)

            expect(nextOption(rows, 2, 1)).toBe(0)
            expect(nextOption(rows, 0, -1)).toBe(2)
        })

        it('skips group headings', () => {
            const rows = toRows(grouped)

            // rows: group, ann, bob, group, cyd
            expect(nextOption(rows, 2, 1)).toBe(4)
            expect(nextOption(rows, 4, -1)).toBe(2)
        })

        it('has nowhere to go in an empty list', () => {
            expect(nextOption([], 0, 1)).toBe(-1)
            expect(nextOption(toRows([{label: 'empty', options: []}]), 0, 1)).toBe(-1)
        })
    })

    describe('firstOption and optionAt', () => {
        it('finds the first thing that can be highlighted', () => {
            expect(firstOption(toRows(flat))).toBe(0)
            expect(firstOption(toRows(grouped))).toBe(1)
            expect(firstOption([])).toBe(-1)
        })

        it('reads an option out, and nothing out of a heading', () => {
            const rows = toRows(grouped)

            expect(optionAt(rows, 1)?.id).toBe('ann')
            expect(optionAt(rows, 0)).toBeUndefined()
            expect(optionAt(rows, 99)).toBeUndefined()
        })
    })

    describe('keyIntent', () => {
        it('opens a closed list with either arrow', () => {
            expect(keyIntent('ArrowDown', false, false)).toBe('open')
            expect(keyIntent('ArrowUp', false, false)).toBe('open')
        })

        it('moves through an open one', () => {
            expect(keyIntent('ArrowDown', true, false)).toBe('next')
            expect(keyIntent('ArrowUp', true, false)).toBe('previous')
        })

        it('only chooses and closes while open', () => {
            expect(keyIntent('Enter', true, false)).toBe('choose')
            expect(keyIntent('Enter', false, false)).toBe('none')
            expect(keyIntent('Escape', true, false)).toBe('close')
            expect(keyIntent('Escape', false, false)).toBe('none')
        })

        // Backspace belongs to the text as long as there is any.
        it('deletes the last value only on an empty query', () => {
            expect(keyIntent('Backspace', false, false)).toBe('removeLast')
            expect(keyIntent('Backspace', false, true)).toBe('none')
        })

        it('means nothing by an ordinary letter', () => {
            expect(keyIntent('a', true, false)).toBe('none')
        })
    })

    describe('defaultMatches', () => {
        it('is a case-insensitive substring', () => {
            expect(defaultMatches('Board members', 'MEMB')).toBe(true)
            expect(defaultMatches('Board members', 'x')).toBe(false)
        })
    })
})
