// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.
import {
    $createParagraphNode,
    $createRangeSelection,
    $createTextNode,
    $getRoot,
    $isRangeSelection,
    $getSelection,
    $setSelection,
    createEditor,
    LexicalEditor,
    ParagraphNode,
    TextNode,
} from 'lexical'

import {$rebuildStyledContent, $setEditorMarkdown} from './liveMarkdown'

const newEditor = (): LexicalEditor => {
    const editor = createEditor({
        namespace: 'test',
        nodes: [],
        onError: (e) => {
            throw e
        },
    })
    return editor
}

// Seed the editor with a single paragraph holding raw (unstyled) text where the
// caret sits at absolute offset `caret` within `text`.
const seed = (editor: LexicalEditor, text: string, caret: number) => {
    editor.update(
        () => {
            const root = $getRoot()
            root.clear()
            const p = $createParagraphNode()
            const t = $createTextNode(text)
            p.append(t)
            root.append(p)
            const sel = $createRangeSelection()
            sel.anchor.set(t.getKey(), caret, 'text')
            sel.focus.set(t.getKey(), caret, 'text')
            $setSelection(sel)
        },
        {discrete: true},
    )
}

// Read the absolute caret offset within the root text content.
const caretAbs = (editor: LexicalEditor): number =>
    editor.getEditorState().read(() => {
        const sel = $getSelection()
        if (!$isRangeSelection(sel)) {
            return -1
        }
        const root = $getRoot()
        const children = root.getChildren()[0] instanceof ParagraphNode ? (root.getChildren()[0] as ParagraphNode).getChildren() : []
        let abs = 0
        const anchor = sel.anchor
        if (anchor.type === 'element') {
            for (let i = 0; i < anchor.offset && i < children.length; i++) {
                abs += children[i].getTextContentSize()
            }
            return abs
        }
        for (const c of children) {
            if (c.getKey() === anchor.getNode().getKey()) {
                return abs + anchor.offset
            }
            abs += c.getTextContentSize()
        }
        return abs
    })

describe('liveMarkdown Lexical integration', () => {
    test('rebuild styles bold text and keeps caret position', () => {
        const editor = newEditor()
        seed(editor, '**bold**', 8) // caret at end
        editor.update(() => $rebuildStyledContent(), {discrete: true})

        editor.getEditorState().read(() => {
            const p = $getRoot().getChildren()[0] as ParagraphNode
            const textNodes = p.getChildren().filter((n) => n instanceof TextNode) as TextNode[]

            // full text preserved
            expect($getRoot().getTextContent()).toBe('**bold**')

            // there is a styled node carrying bold
            const styled = textNodes.map((n) => n.getStyle())
            expect(styled.some((s) => s.includes('font-weight: bold'))).toBe(true)

            // delimiters get an opacity style
            expect(styled.some((s) => s.includes('opacity: 0.4'))).toBe(true)
        })

        expect(caretAbs(editor)).toBe(8)
    })

    test('caret in the middle is preserved across rebuild', () => {
        const editor = newEditor()
        seed(editor, '*hi* there', 6) // caret just after the styled '*hi* '
        editor.update(() => $rebuildStyledContent(), {discrete: true})
        expect(caretAbs(editor)).toBe(6)
        editor.getEditorState().read(() => {
            expect($getRoot().getTextContent()).toBe('*hi* there')
        })
    })

    test('multi-line content round-trips with single newlines', () => {
        const editor = newEditor()
        editor.update(() => $setEditorMarkdown('# Title\n\nsome **text**'), {discrete: true})
        editor.update(() => $rebuildStyledContent(), {discrete: true})
        editor.getEditorState().read(() => {
            expect($getRoot().getTextContent()).toBe('# Title\n\nsome **text**')
        })
    })

    test('rebuild is idempotent (text unchanged on second pass)', () => {
        const editor = newEditor()
        seed(editor, 'a **b** c', 9)
        editor.update(() => $rebuildStyledContent(), {discrete: true})
        const first = editor.getEditorState().read(() => $getRoot().getTextContent())
        editor.update(() => $rebuildStyledContent(), {discrete: true})
        const second = editor.getEditorState().read(() => $getRoot().getTextContent())
        expect(second).toBe(first)
        expect(second).toBe('a **b** c')
    })
})
