// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.
import {bindHotkey, isTypingTarget, matchesHotkey} from './hotkeys'

const press = (init: KeyboardEventInit) => new KeyboardEvent('keydown', init)

describe('hotkeys', () => {
    describe('matching', () => {
        it('matches a named key', () => {
            expect(matchesHotkey(press({key: 'Escape'}), 'esc')).toBe(true)
            expect(matchesHotkey(press({key: 'Enter'}), 'esc')).toBe(false)
        })

        it('takes any of the comma-separated alternatives', () => {
            expect(matchesHotkey(press({key: 'Delete'}), 'del,backspace')).toBe(true)
            expect(matchesHotkey(press({key: 'Backspace'}), 'del,backspace')).toBe(true)
        })

        it('matches modifiers exactly, so undo is not redo', () => {
            const undo = press({code: 'KeyZ', ctrlKey: true})
            const redo = press({code: 'KeyZ', ctrlKey: true, shiftKey: true})

            expect(matchesHotkey(undo, 'ctrl+z,cmd+z')).toBe(true)
            expect(matchesHotkey(undo, 'shift+ctrl+z,shift+cmd+z')).toBe(false)

            expect(matchesHotkey(redo, 'ctrl+z,cmd+z')).toBe(false)
            expect(matchesHotkey(redo, 'shift+ctrl+z,shift+cmd+z')).toBe(true)
        })

        it('does not fire a bare key while a modifier is held', () => {
            expect(matchesHotkey(press({key: 'Escape', ctrlKey: true}), 'esc')).toBe(false)
        })

        // The reason letters are matched on .code: under a Cyrillic layout the
        // browser reports key 'в' for the same physical key that carries 'd'.
        it('matches a letter by its physical key, whatever the layout', () => {
            expect(matchesHotkey(press({code: 'KeyD', key: 'd', ctrlKey: true}), 'ctrl+d')).toBe(true)
            expect(matchesHotkey(press({code: 'KeyD', key: 'в', ctrlKey: true}), 'ctrl+d')).toBe(true)
        })

        it('matches a letter shifted into upper case', () => {
            expect(matchesHotkey(press({code: 'KeyF', key: 'F', ctrlKey: true, shiftKey: true}), 'ctrl+shift+f')).toBe(true)
        })
    })

    describe('isTypingTarget', () => {
        it('counts the tags a shortcut must not reach through', () => {
            expect(isTypingTarget(document.createElement('input'))).toBe(true)
            expect(isTypingTarget(document.createElement('textarea'))).toBe(true)
            expect(isTypingTarget(document.createElement('select'))).toBe(true)
            expect(isTypingTarget(document.createElement('div'))).toBe(false)
        })

        it('counts a contenteditable', () => {
            const div = document.createElement('div')
            div.contentEditable = 'true'

            // jsdom does not derive isContentEditable from the attribute.
            Object.defineProperty(div, 'isContentEditable', {value: true})

            expect(isTypingTarget(div)).toBe(true)
        })

        it('is false without a target', () => {
            expect(isTypingTarget(null)).toBe(false)
        })
    })

    describe('bindHotkey', () => {
        it('calls the handler and unbinds', () => {
            const handler = jest.fn()
            const unbind = bindHotkey('esc', handler)

            document.dispatchEvent(press({key: 'Escape'}))
            expect(handler).toHaveBeenCalledTimes(1)

            unbind()
            document.dispatchEvent(press({key: 'Escape'}))
            expect(handler).toHaveBeenCalledTimes(1)
        })

        it('leaves the shortcut to the field being typed in', () => {
            const handler = jest.fn()
            const input = document.createElement('input')
            document.body.appendChild(input)

            const unbind = bindHotkey('esc', handler)
            input.dispatchEvent(press({key: 'Escape', bubbles: true}))

            expect(handler).not.toHaveBeenCalled()

            unbind()
            input.remove()
        })

        it('ignores a shortcut it cannot parse', () => {
            const handler = jest.fn()
            const unbind = bindHotkey('', handler)

            document.dispatchEvent(press({key: 'Escape'}))
            expect(handler).not.toHaveBeenCalled()

            unbind()
        })
    })
})
