// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

// Lexical integration of the live-markdown highlighting. The editor runs in
// plain-text mode, so all content lives in a single paragraph whose lines are
// separated by LineBreakNodes (this is what keeps `getTextContent()` producing
// single-'\n' separated markdown, matching the stored format). Because we cannot
// give each line its own block element in that model, styling is expressed
// entirely as per-character inline styles computed by `computeStyledLines`.
//
// On every content change we rebuild the paragraph's children from the current
// text, splitting it into styled TextNodes and re-inserting LineBreakNodes, then
// restore the caret/selection by absolute character offset. Rebuilds are guarded
// by a tag so they don't recurse, and skipped during IME composition.

import * as React from 'react'

import {
    $createLineBreakNode,
    $createParagraphNode,
    $createRangeSelection,
    $createTextNode,
    $getRoot,
    $getSelection,
    $isRangeSelection,
    $setSelection,
    ElementNode,
    LexicalEditor,
    LexicalNode,
    Point,
    RangeSelection,
    TextNode,
} from 'lexical'

import {computeStyledLines, StyledLine} from './markdownStyling'

const REBUILD_TAG = 'live-markdown'

const camelToKebab = (s: string) => s.replace(/[A-Z]/g, (m) => '-' + m.toLowerCase())

const styleToCss = (style: React.CSSProperties): string =>
    Object.entries(style).
        map(([k, v]) => `${camelToKebab(k)}: ${v}`).
        join('; ')

// The absolute character index of a selection point within the root's text
// content (the same string `getTextContent()` returns). Line breaks count as one
// character, mirroring the '\n' they serialize to.
const pointToAbsolute = (root: ElementNode, point: Point): number => {
    const children = root.getChildren()
    if (point.type === 'element') {
        // Element point on the paragraph: offset is a child index.
        let abs = 0
        for (let i = 0; i < point.offset && i < children.length; i++) {
            abs += children[i].getTextContentSize()
        }
        return abs
    }

    // Text point: sum the sizes of preceding siblings + local offset.
    let abs = 0
    for (const child of children) {
        if (child.getKey() === point.getNode().getKey()) {
            return abs + point.offset
        }
        abs += child.getTextContentSize()
    }
    return abs
}

// Resolve an absolute character index back to a concrete (node, offset) point in
// the freshly rebuilt paragraph. Prefers landing on a TextNode; falls back to an
// element point on the paragraph for positions on an empty line.
const absoluteToPoint = (
    paragraph: ElementNode,
    abs: number,
): {node: LexicalNode, offset: number, type: 'text' | 'element'} => {
    const children = paragraph.getChildren()
    let running = 0
    for (let i = 0; i < children.length; i++) {
        const child = children[i]
        const size = child.getTextContentSize()
        if (child instanceof TextNode) {
            if (abs <= running + size) {
                return {node: child, offset: abs - running, type: 'text'}
            }
            running += size
        } else {
            // LineBreakNode (size 1). A caret exactly before it lands as an element
            // point at this child's index (start of the empty/next line).
            if (abs <= running) {
                return {node: paragraph, offset: i, type: 'element'}
            }
            running += size
        }
    }

    // Past the end: element point at the very end of the paragraph.
    return {node: paragraph, offset: children.length, type: 'element'}
}

// Build the TextNodes for a single styled line (no trailing line break).
const buildLineNodes = (line: StyledLine): TextNode[] => {
    const nodes: TextNode[] = []
    let pointer = 0
    for (const seg of line.segments) {
        if (seg.start > pointer) {
            nodes.push($createTextNode(line.text.slice(pointer, seg.start)))
        }
        const styled = $createTextNode(line.text.slice(seg.start, seg.end))
        styled.setStyle(styleToCss(seg.style))
        nodes.push(styled)
        pointer = seg.end
    }
    if (pointer < line.text.length) {
        nodes.push($createTextNode(line.text.slice(pointer)))
    }
    return nodes
}

// Rebuild the whole document as a single paragraph of styled lines. Assumes it is
// called inside an editor.update().
export const $rebuildStyledContent = (): void => {
    const root = $getRoot()
    const fullText = root.getTextContent()

    // In plain-text mode all leaves live in a single paragraph; use it as the
    // container for translating the selection to/from absolute offsets.
    const oldParagraph = root.getFirstChild()
    const selection = $getSelection()
    let anchorAbs: number | null = null
    let focusAbs: number | null = null
    if ($isRangeSelection(selection) && oldParagraph instanceof ElementNode) {
        anchorAbs = pointToAbsolute(oldParagraph, selection.anchor)
        focusAbs = pointToAbsolute(oldParagraph, selection.focus)
    }

    const styledLines = computeStyledLines(fullText)

    const paragraph = $createParagraphNode()
    styledLines.forEach((line, i) => {
        if (i > 0) {
            paragraph.append($createLineBreakNode())
        }
        for (const node of buildLineNodes(line)) {
            paragraph.append(node)
        }
    })

    root.clear()
    root.append(paragraph)

    if (anchorAbs !== null && focusAbs !== null) {
        const a = absoluteToPoint(paragraph, anchorAbs)
        const f = absoluteToPoint(paragraph, focusAbs)
        const newSelection: RangeSelection = $createRangeSelection()
        newSelection.anchor.set(a.node.getKey(), a.offset, a.type)
        newSelection.focus.set(f.node.getKey(), f.offset, f.type)
        $setSelection(newSelection)
    }
}

// Replace the editor content with the given markdown text (used for
// initialisation from the `initialText` prop). Caret is placed at the end.
export const $setEditorMarkdown = (text: string): void => {
    const root = $getRoot()
    root.clear()
    const paragraph = $createParagraphNode()
    const lines = text.split('\n')
    lines.forEach((lineText, i) => {
        if (i > 0) {
            paragraph.append($createLineBreakNode())
        }
        if (lineText.length > 0) {
            paragraph.append($createTextNode(lineText))
        }
    })
    root.append(paragraph)
    root.selectEnd()
}

// Register live-markdown styling on an editor. Returns an unregister function.
export const registerLiveMarkdown = (editor: LexicalEditor): (() => void) => {
    return editor.registerUpdateListener(({tags, dirtyElements, dirtyLeaves}) => {
        if (tags.has(REBUILD_TAG)) {
            return
        }
        if (dirtyElements.size === 0 && dirtyLeaves.size === 0) {
            return
        }
        if (editor.isComposing()) {
            return
        }
        editor.update(
            () => {
                $rebuildStyledContent()
            },
            {tag: REBUILD_TAG},
        )
    })
}
