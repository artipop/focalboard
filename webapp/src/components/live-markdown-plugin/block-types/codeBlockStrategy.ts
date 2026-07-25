// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.
import {BlockStrategy} from '../pluginStrategy'

const createCodeBlockStrategy = (): BlockStrategy => {
    const blockType = 'code-block'
    const CODE_BLOCK_DELIMITER = /^```/

    return {
        type: blockType,
        className: 'code-block',

        // A line is part of a fenced code block if it falls within a *closed* pair
        // of ``` fence lines (inclusive of both fences). Fence lines are paired in
        // document order; an unclosed trailing fence does not start a code block.
        mapLineType: (text, lineIndex, lines) => {
            const fenceIndices: number[] = []
            for (let i = 0; i < lines.length; i++) {
                if (CODE_BLOCK_DELIMITER.test(lines[i])) {
                    fenceIndices.push(i)
                }
            }

            for (let p = 0; p + 1 < fenceIndices.length; p += 2) {
                if (lineIndex >= fenceIndices[p] && lineIndex <= fenceIndices[p + 1]) {
                    return blockType
                }
            }

            return ''
        },
    }
}

export default createCodeBlockStrategy
