// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

// Framework-agnostic core of the live-markdown highlighting. Given the full
// editor text it splits it into lines, classifies each line's block type
// (heading / code-block), and computes the styled character segments for each
// line by running the inline-style strategies. The editor layer (Lexical)
// turns these segments into styled text nodes; keeping this pure makes the
// styling logic unit-testable without any editor framework.

import * as React from 'react'

import {InlineStrategy, BlockStrategy} from './pluginStrategy'

import createBoldStyleStrategy from './inline-styles/boldStyleStrategy'
import createItalicStyleStrategy from './inline-styles/italicStyleStrategy'
import createStrikethroughStyleStrategy from './inline-styles/strikethroughStyleStrategy'
import createHeadingDelimiterStyleStrategy from './inline-styles/headingDelimiterStyleStrategy'
import createULDelimiterStyleStrategy from './inline-styles/ulDelimiterStyleStrategy'
import createOLDelimiterStyleStrategy from './inline-styles/olDelimiterStyleStrategy'
import createQuoteStyleStrategy from './inline-styles/quoteStyleStrategy'
import createInlineCodeStyleStrategy from './inline-styles/inlineCodeStyleStrategy'

import createCodeBlockStrategy from './block-types/codeBlockStrategy'
import createHeadingBlockStrategy from './block-types/headingBlockStrategy'

export interface StyledSegment {
    start: number // inclusive
    end: number // exclusive
    style: React.CSSProperties
}

export interface StyledLine {
    text: string
    blockType: string
    className: string
    segments: StyledSegment[]
}

// Inline strategies, applied in order. Later strategies merge their CSS on top
// of earlier ones (so a character can be e.g. bold and italic at once), matching
// the original draft-js customStyleMap behaviour.
const inlineStrategies: InlineStrategy[] = [
    createBoldStyleStrategy(),
    createItalicStyleStrategy(),
    createStrikethroughStyleStrategy(),
    createHeadingDelimiterStyleStrategy(),
    createULDelimiterStyleStrategy(),
    createOLDelimiterStyleStrategy(),
    createQuoteStyleStrategy(),
    createInlineCodeStyleStrategy(),
]

// Block strategies, applied in precedence order: the first strategy that claims
// a line wins. Code-block is checked before heading so a `# ...` line inside a
// fenced code block stays a code line rather than becoming a heading.
const blockStrategies: BlockStrategy[] = [
    createCodeBlockStrategy(),
    createHeadingBlockStrategy(),
]

const classifyLine = (text: string, lineIndex: number, lines: string[]): {blockType: string, className: string} => {
    for (const strategy of blockStrategies) {
        const blockType = strategy.mapLineType(text, lineIndex, lines)
        if (blockType) {
            return {blockType, className: strategy.className}
        }
    }
    return {blockType: '', className: ''}
}

const applyRanges = (
    perChar: Array<React.CSSProperties | null>,
    ranges: number[][],
    style?: React.CSSProperties,
) => {
    if (!style) {
        return
    }
    for (const [start, end] of ranges) {
        for (let i = start; i <= end && i < perChar.length; i++) {
            perChar[i] = {...(perChar[i] || {}), ...style}
        }
    }
}

// draft-js rendered heading blocks as <h1>..<h6> and code blocks as monospace
// via their block type. In Lexical's single-paragraph plain-text model we cannot
// give each line its own block element, so we reproduce that block-level look as a
// base inline style applied to the whole line.
const HEADING_BASE_STYLES: Record<string, React.CSSProperties> = {
    'header-one': {fontSize: '2em', fontWeight: 'bold'},
    'header-two': {fontSize: '1.5em', fontWeight: 'bold'},
    'header-three': {fontSize: '1.17em', fontWeight: 'bold'},
    'header-four': {fontSize: '1em', fontWeight: 'bold'},
    'header-five': {fontSize: '0.83em', fontWeight: 'bold'},
    'header-six': {fontSize: '0.67em', fontWeight: 'bold'},
}

const blockBaseStyle = (blockType: string): React.CSSProperties | null => {
    if (blockType === 'code-block') {
        return {fontFamily: 'monospace'}
    }
    return HEADING_BASE_STYLES[blockType] || null
}

const styleLine = (text: string, blockType: string): StyledSegment[] => {
    const base = blockBaseStyle(blockType)
    const perChar: Array<React.CSSProperties | null> = new Array(text.length).
        fill(null).
        map(() => (base ? {...base} : null))

    for (const strategy of inlineStrategies) {
        const styleRanges = strategy.findStyleRanges(text, blockType)
        applyRanges(perChar, styleRanges, strategy.styles)

        if (strategy.findDelimiterRanges) {
            const delimiterRanges = strategy.findDelimiterRanges(text, styleRanges)
            applyRanges(perChar, delimiterRanges, strategy.delimiterStyles)
        }
    }

    // Coalesce consecutive characters carrying an identical style into segments.
    const segments: StyledSegment[] = []
    let runStart = 0
    const key = (s: React.CSSProperties | null) => (s ? JSON.stringify(s, Object.keys(s).sort()) : '')
    for (let i = 1; i <= perChar.length; i++) {
        if (i === perChar.length || key(perChar[i]) !== key(perChar[runStart])) {
            const style = perChar[runStart]
            if (style) {
                segments.push({start: runStart, end: i, style})
            }
            runStart = i
        }
    }
    return segments
}

// Split the editor text on newlines and compute the styled representation of
// each resulting line.
export const computeStyledLines = (fullText: string): StyledLine[] => {
    const lines = fullText.split('\n')
    return lines.map((text, i) => {
        const {blockType, className} = classifyLine(text, i, lines)
        return {
            text,
            blockType,
            className,
            segments: styleLine(text, blockType),
        }
    })
}
