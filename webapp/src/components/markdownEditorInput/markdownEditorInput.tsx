// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.
import React, {MutableRefObject, ReactElement, useEffect, useRef} from 'react'

import {LexicalComposer} from '@lexical/react/LexicalComposer'
import {ContentEditable} from '@lexical/react/LexicalContentEditable'
import {PlainTextPlugin} from '@lexical/react/LexicalPlainTextPlugin'
import {HistoryPlugin} from '@lexical/react/LexicalHistoryPlugin'
import {OnChangePlugin} from '@lexical/react/LexicalOnChangePlugin'
import {LexicalErrorBoundary} from '@lexical/react/LexicalErrorBoundary'
import {useLexicalComposerContext} from '@lexical/react/LexicalComposerContext'
import {mergeRegister} from '@lexical/utils'
import {
    $getRoot,
    BLUR_COMMAND,
    COMMAND_PRIORITY_LOW,
    EditorState,
    FOCUS_COMMAND,
    KEY_BACKSPACE_COMMAND,
    KEY_ENTER_COMMAND,
    KEY_ESCAPE_COMMAND,
} from 'lexical'

import {Utils} from '../../utils'

import {$rebuildStyledContent, $setEditorMarkdown, registerLiveMarkdown} from '../live-markdown-plugin/liveMarkdown'

import MentionsPlugin from './plugins/mentionsPlugin'
import EmojiPlugin from './plugins/emojiPlugin'

import './markdownEditorInput.scss'

type Props = {
    onChange?: (text: string) => void
    onFocus?: () => void
    onBlur?: (text: string) => void
    onEditorCancel?: () => void
    initialText?: string
    id?: string
    isEditing: boolean
    saveOnEnter?: boolean
}

// Registers live-markdown styling on the editor and styles the initial content.
const LiveMarkdownPlugin = (): null => {
    const [editor] = useLexicalComposerContext()
    useEffect(() => {
        const unregister = registerLiveMarkdown(editor)
        editor.update(() => $rebuildStyledContent(), {tag: 'live-markdown'})
        return unregister
    }, [editor])
    return null
}

type EventsProps = {
    onFocus?: () => void
    onBlur?: (text: string) => void
    onEditorCancel?: () => void
    saveOnEnter?: boolean
    suppressBlurRef: MutableRefObject<boolean>
}

// Focus on mount and translate editor keyboard/focus events into the callbacks
// the surrounding board UI expects (Esc → blur, Enter → save, Backspace on empty
// → cancel).
const EditorEventsPlugin = (props: EventsProps): null => {
    const {onFocus, onBlur, onEditorCancel, saveOnEnter, suppressBlurRef} = props
    const [editor] = useLexicalComposerContext()

    useEffect(() => {
        editor.focus()
    }, [editor])

    useEffect(() => {
        const readText = () => editor.getEditorState().read(() => $getRoot().getTextContent())

        return mergeRegister(
            editor.registerCommand(
                KEY_ESCAPE_COMMAND,
                () => {
                    editor.blur()
                    return true
                },
                COMMAND_PRIORITY_LOW,
            ),
            editor.registerCommand<KeyboardEvent | null>(
                KEY_ENTER_COMMAND,
                (event) => {
                    if (saveOnEnter && event && !event.shiftKey) {
                        event.preventDefault()
                        onBlur?.(readText())
                        return true
                    }
                    return false
                },
                COMMAND_PRIORITY_LOW,
            ),
            editor.registerCommand<KeyboardEvent>(
                KEY_BACKSPACE_COMMAND,
                () => {
                    if (onEditorCancel && $getRoot().getTextContent().length === 0) {
                        onEditorCancel()
                        return true
                    }
                    return false
                },
                COMMAND_PRIORITY_LOW,
            ),
            editor.registerCommand(
                FOCUS_COMMAND,
                () => {
                    onFocus?.()
                    return false
                },
                COMMAND_PRIORITY_LOW,
            ),
            editor.registerCommand(
                BLUR_COMMAND,
                () => {
                    if (!suppressBlurRef.current) {
                        onBlur?.(readText())
                    }
                    return false
                },
                COMMAND_PRIORITY_LOW,
            ),
        )
    }, [editor, onBlur, onFocus, onEditorCancel, saveOnEnter, suppressBlurRef])

    return null
}

const MarkdownEditorInput = (props: Props): ReactElement => {
    const {onChange, onFocus, onBlur, onEditorCancel, initialText, id, saveOnEnter} = props

    // Guards the blur-save while the "add user to board" confirm dialog (opened
    // from the mentions plugin) steals focus.
    const suppressBlurRef = useRef(false)
    const lastTextRef = useRef<string>(initialText || '')

    const initialConfig = {
        namespace: id || 'MarkdownEditorInput',
        onError: (error: Error) => {
            Utils.logError(`Lexical editor error: ${error.message}`)
        },
        editable: true,
        editorState: () => $setEditorMarkdown(initialText || ''),
    }

    const handleChange = (editorState: EditorState) => {
        const text = editorState.read(() => $getRoot().getTextContent())
        if (text !== lastTextRef.current) {
            lastTextRef.current = text
            onChange?.(text)
        }
    }

    return (
        <div className='MarkdownEditorInput'>
            <LexicalComposer initialConfig={initialConfig}>
                <PlainTextPlugin
                    contentEditable={<ContentEditable className='MarkdownEditorInput__content'/>}
                    placeholder={null}
                    ErrorBoundary={LexicalErrorBoundary}
                />
                {/*
                  * TODO: undo/redo does not actually work here since the draft-js ->
                  * Lexical migration (96cd8494). Ctrl/Cmd+Z is a no-op: not just after a
                  * cut, but for plain typing too. Confirmed outside of test-runner
                  * synthetic events, with CDP-level key presses, for both Meta+Z and
                  * Ctrl+Z. HistoryPlugin is mounted, so the wiring looks correct and the
                  * cause is still unknown -- LiveMarkdownPlugin below is the first
                  * suspect, being the one plugin that rewrites content on every change.
                  * The E2E test covering this (GH-2520 in cypress/e2e/createBoard.ts) is
                  * skipped until it works again.
                  */}
                <HistoryPlugin/>
                <OnChangePlugin
                    onChange={handleChange}
                    ignoreSelectionChange={true}
                />
                <LiveMarkdownPlugin/>
                <EditorEventsPlugin
                    onFocus={onFocus}
                    onBlur={onBlur}
                    onEditorCancel={onEditorCancel}
                    saveOnEnter={saveOnEnter}
                    suppressBlurRef={suppressBlurRef}
                />
                <MentionsPlugin suppressBlurRef={suppressBlurRef}/>
                <EmojiPlugin/>
            </LexicalComposer>
        </div>
    )
}

export default MarkdownEditorInput
