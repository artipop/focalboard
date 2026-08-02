// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.
import React, {ReactElement, useCallback, useEffect, useState} from 'react'
import ReactDOM from 'react-dom'

import {SearchIndex} from 'emoji-mart'

import {useLexicalComposerContext} from '@lexical/react/LexicalComposerContext'
import {
    LexicalTypeaheadMenuPlugin,
    MenuOption,
    useBasicTypeaheadTriggerMatch,
} from '@lexical/react/LexicalTypeaheadMenuPlugin'
import {$createTextNode, $getSelection, $isRangeSelection, TextNode} from 'lexical'

type EmojiResult = {
    id: string
    name: string
    colons: string
    native: string
}

class EmojiTypeaheadOption extends MenuOption {
    emoji: EmojiResult

    constructor(emoji: EmojiResult) {
        super(emoji.id)
        this.emoji = emoji
    }
}

const MAX_EMOJI_SUGGESTIONS = 10

// `:`-triggered emoji autocomplete backed by emoji-mart's search index. Inserts
// the picked emoji's native glyph, replacing the `:query` trigger text.
const EmojiPlugin = (): ReactElement => {
    const [editor] = useLexicalComposerContext()
    const [query, setQuery] = useState<string | null>(null)

    const triggerFn = useBasicTypeaheadTriggerMatch(':', {minLength: 1})

    // emoji-mart 5 searches asynchronously and puts the glyph on the emoji's
    // first skin rather than on the emoji itself.
    const [options, setOptions] = useState<EmojiTypeaheadOption[]>([])

    useEffect(() => {
        if (!query) {
            setOptions([])
            return undefined
        }

        let cancelled = false
        SearchIndex.search(query).then((results: Array<{id: string, name: string, skins?: Array<{native?: string}>}>) => {
            if (cancelled) {
                return
            }
            const found = (results || []).
                slice(0, MAX_EMOJI_SUGGESTIONS).
                map((e): EmojiResult => ({id: e.id, name: e.name, colons: `:${e.id}:`, native: e.skins?.[0]?.native || ''})).
                filter((e) => Boolean(e.native)).
                map((e) => new EmojiTypeaheadOption(e))
            setOptions(found)
        })

        return () => {
            cancelled = true
        }
    }, [query])

    const onSelectOption = useCallback((
        selectedOption: EmojiTypeaheadOption,
        nodeToReplace: TextNode | null,
        closeMenu: () => void,
    ) => {
        editor.update(() => {
            const emojiText = `${selectedOption.emoji.native} `
            const newNode = $createTextNode(emojiText)
            if (nodeToReplace && nodeToReplace.isAttached()) {
                nodeToReplace.replace(newNode)
                newNode.selectNext(0, 0)
            } else {
                const selection = $getSelection()
                if ($isRangeSelection(selection)) {
                    selection.insertText(emojiText)
                }
            }
        })
        closeMenu()
    }, [editor])

    return (
        <LexicalTypeaheadMenuPlugin<EmojiTypeaheadOption>
            options={options}
            onQueryChange={setQuery}
            onSelectOption={onSelectOption}
            triggerFn={triggerFn}
            menuRenderFn={(
                anchorElementRef: React.RefObject<HTMLElement | null>,
                {selectedIndex, selectOptionAndCleanUp, setHighlightedIndex}: {
                    selectedIndex: number | null
                    selectOptionAndCleanUp: (option: EmojiTypeaheadOption) => void
                    setHighlightedIndex: (index: number) => void
                },
            ) => {
                if (!anchorElementRef.current || options.length === 0) {
                    return null
                }
                return ReactDOM.createPortal(
                    <div className='MarkdownEditorInput--emojis'>
                        <div role='listbox'>
                            {options.map((option, i) => (
                                <div
                                    key={option.emoji.id}
                                    role='option'
                                    aria-selected={selectedIndex === i}
                                    className={`EmojiEntry ${selectedIndex === i ? 'EmojiEntry--selected' : ''}`}
                                    onMouseDown={(e) => e.preventDefault()}
                                    onMouseEnter={() => setHighlightedIndex(i)}
                                    onClick={() => {
                                        setHighlightedIndex(i)
                                        selectOptionAndCleanUp(option)
                                    }}
                                >
                                    <span className='EmojiEntry__native'>{option.emoji.native}</span>
                                    <span className='EmojiEntry__colons'>{option.emoji.colons}</span>
                                </div>
                            ))}
                        </div>
                    </div>,
                    anchorElementRef.current,
                )
            }}
        />
    )
}

export default EmojiPlugin
