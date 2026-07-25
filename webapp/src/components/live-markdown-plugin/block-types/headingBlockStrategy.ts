// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.
import {BlockStrategy} from '../pluginStrategy'

const createHeadingBlockStrategy = (): BlockStrategy => {
    const HEADING_REGEX = /^(#{1,6})\s/
    const HEADING_LEVELS = [
        'header-one',
        'header-two',
        'header-three',
        'header-four',
        'header-five',
        'header-six',
    ]

    return {
        type: 'heading',
        className: 'heading-block',

        // Classify a single line: if it starts with 1-6 '#' followed by whitespace,
        // it is a heading of the corresponding level; otherwise it is a plain line.
        mapLineType: (text) => {
            const match = HEADING_REGEX.exec(text)
            if (!match) {
                return ''
            }
            const headingLevel = match[1].length
            return HEADING_LEVELS[headingLevel - 1]
        },
    }
}

export default createHeadingBlockStrategy
