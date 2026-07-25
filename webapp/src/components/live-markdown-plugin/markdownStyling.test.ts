// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.
import {computeStyledLines, StyledLine} from './markdownStyling'

// Return the merged style object that applies at character index `i` of the line.
const styleAt = (line: StyledLine, i: number) => {
    const seg = line.segments.find((s) => i >= s.start && i < s.end)
    return seg ? seg.style : undefined
}

describe('live-markdown markdownStyling', () => {
    test('bold: whole run is bold, delimiters dimmed', () => {
        const [line] = computeStyledLines('**bold**')
        expect(line.blockType).toBe('')

        // 'b' of bold (index 2) is bold, not dimmed
        expect(styleAt(line, 2)).toMatchObject({fontWeight: 'bold'})
        expect(styleAt(line, 2)?.opacity).toBeUndefined()

        // leading '*' (index 0) is bold and dimmed
        expect(styleAt(line, 0)).toMatchObject({fontWeight: 'bold', opacity: 0.4})
    })

    test('italic: run italic, delimiters dimmed', () => {
        const [line] = computeStyledLines('_italic_')
        expect(styleAt(line, 1)).toMatchObject({fontStyle: 'italic'})
        expect(styleAt(line, 0)).toMatchObject({fontStyle: 'italic', opacity: 0.4})
    })

    test('strikethrough: line-through with delimiters dimmed', () => {
        const [line] = computeStyledLines('~~gone~~')
        expect(styleAt(line, 2)).toMatchObject({textDecoration: 'line-through'})
        expect(styleAt(line, 0)).toMatchObject({opacity: 0.4})
    })

    test('bold + italic nest: character is both', () => {
        const [line] = computeStyledLines('***x***')
        expect(styleAt(line, 3)).toMatchObject({fontWeight: 'bold', fontStyle: 'italic'})
    })

    test('heading: block type + level, sized text, delimiter dimmed', () => {
        const [line] = computeStyledLines('## Title')
        expect(line.blockType).toBe('header-two')
        expect(line.className).toBe('heading-block')

        // title text carries the heading base size
        expect(styleAt(line, 3)).toMatchObject({fontSize: '1.5em', fontWeight: 'bold'})

        // '#' delimiter dimmed (on top of the base heading style)
        expect(styleAt(line, 0)).toMatchObject({opacity: 0.4})
    })

    test('unordered list delimiter is bold', () => {
        const [line] = computeStyledLines('* item')
        expect(styleAt(line, 0)).toMatchObject({fontWeight: 'bold'})
    })

    test('ordered list delimiter is bold', () => {
        const [line] = computeStyledLines('1. item')
        expect(styleAt(line, 0)).toMatchObject({fontWeight: 'bold'})
    })

    test('quote: styled text with dimmed marker', () => {
        const [line] = computeStyledLines('> quoted')
        expect(styleAt(line, 3)).toMatchObject({opacity: 0.75})
        expect(styleAt(line, 0)).toMatchObject({opacity: 0.4})
    })

    test('inline code is monospace', () => {
        const [line] = computeStyledLines('a `code` b')
        expect(styleAt(line, 3)).toMatchObject({fontFamily: 'monospace'})
    })

    test('fenced code block spans lines between closed fences', () => {
        const lines = computeStyledLines('```\ncode\n```')
        expect(lines[0].blockType).toBe('code-block')
        expect(lines[1].blockType).toBe('code-block')
        expect(lines[2].blockType).toBe('code-block')
    })

    test('unclosed fence does not start a code block', () => {
        const lines = computeStyledLines('```\ncode')
        expect(lines[0].blockType).toBe('')
        expect(lines[1].blockType).toBe('')
    })

    test('heading inside fenced code block stays a code line', () => {
        const lines = computeStyledLines('```\n# not a heading\n```')
        expect(lines[1].blockType).toBe('code-block')
    })

    test('inline code disabled inside code block: line is uniformly monospace', () => {
        const lines = computeStyledLines('```\na `x` b\n```')

        // whole code line is monospace from the block base style, and the inline-code
        // strategy adds no extra sub-segments (backticks are not specially styled),
        // so the line collapses to a single uniform segment.
        expect(lines[1].segments).toHaveLength(1)
        expect(styleAt(lines[1], 3)).toMatchObject({fontFamily: 'monospace'})
    })

    test('plain line has no segments', () => {
        const [line] = computeStyledLines('just text')
        expect(line.segments).toHaveLength(0)
    })
})
