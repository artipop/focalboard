// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.
import React, {type JSX, FC, useEffect, useRef} from 'react'
import {Picker} from 'emoji-mart'

import './emojiPicker.scss'

type Props = {
    onSelect: (emoji: string) => void
}

type PickedEmoji = {
    native?: string
}

// emoji-mart 5 is not a React component: Picker is a custom element that mounts
// itself into a host node. @emoji-mart/react exists to wrap it but does not
// support React 19, and the wrapper is this small. The data set is registered
// once at startup in main.tsx.
const EmojiPicker: FC<Props> = (props: Props): JSX.Element => {
    const host = useRef<HTMLDivElement>(null)

    // Read through a ref so a new onSelect never remounts the picker, which
    // would throw away the user's search text and scroll position.
    const onSelect = useRef(props.onSelect)
    onSelect.current = props.onSelect

    useEffect(() => {
        const node = host.current
        if (!node) {
            return undefined
        }

        // v5 draws native glyphs instead of the sprite sheet v3 needed, so the
        // 3.7 MB emoji_spirit.png is no longer shipped. It also renders into a
        // shadow root, which is why the .emoji-mart-* overrides that used to sit
        // in emojiPicker.scss cannot reach it any more.
        const picker = new Picker({
            parent: node,
            previewPosition: 'none',
            onEmojiSelect: (emoji: PickedEmoji) => {
                if (emoji.native) {
                    onSelect.current(emoji.native)
                }
            },
        }) as unknown as {remove?: () => void}

        return () => {
            picker.remove?.()
            node.replaceChildren()
        }
    }, [])

    return (
        <div
            className='EmojiPicker'
            onClick={(e) => e.stopPropagation()}
        >
            <div ref={host}/>
        </div>
    )
}

export default EmojiPicker
