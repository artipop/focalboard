// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.
import {InlineStrategy} from '../pluginStrategy'
import findRangesWithRegex from '../utils/findRangesWithRegex'

const createHeadingDelimiterStyleStrategy = (): InlineStrategy => {
    const headingDelimiterRegex = /(^#{1,6})\s/g

    return {
        style: 'HEADING-DELIMITER',
        findStyleRanges: (text, blockType) => {
            // Skip the text search if the block isn't a header block
            if (blockType.indexOf('header') < 0) {
                return []
            }

            const headingDelimiterRanges = findRangesWithRegex(
                text,
                headingDelimiterRegex,
            )
            return headingDelimiterRanges
        },
        styles: {
            opacity: 0.4,
        },
    }
}

export default createHeadingDelimiterStyleStrategy
