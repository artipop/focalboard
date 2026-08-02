// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

// dnd-kit attaches its accessibility plumbing to a draggable from an effect, so
// whether a snapshot catches it depends on when the snapshot was taken -- the
// same test flaps between runs. None of it is markup this codebase wrote, and
// the description id carries a module-global counter that shifts with render
// order on top of that, so the whole set is normalised out of snapshots. The
// behaviour it provides belongs in tests that drive the keyboard, not in
// snapshots of unrelated components.
//
// Hiding them does mean nothing notices if they stop being emitted, and they
// are not decoration: role, tabindex and aria-roledescription are how a
// keyboard reaches a card at all, which is a large part of why this app moved
// off the HTML5 drag API. Covering them needs a browser -- measured here, a
// dedicated test saw no attributes at all on some runs and the full set on
// others. It belongs with the other TODO(react-19) items.

const MARKER = 'aria-roledescription'
const DND_KIT_ATTRIBUTES = [
    'aria-describedby',
    'aria-disabled',
    'aria-grabbed',
    'aria-pressed',
    'aria-roledescription',
    'role',
    'tabindex',
]

function isDndKitDraggable(element: Element): boolean {
    return element.getAttribute(MARKER) === 'draggable'
}

function hasDndKitMarkup(element: Element): boolean {
    return isDndKitDraggable(element) || element.querySelector(`[${MARKER}="draggable"]`) !== null
}

function strip(element: Element): void {
    if (isDndKitDraggable(element)) {
        for (const attribute of DND_KIT_ATTRIBUTES) {
            element.removeAttribute(attribute)
        }
    }
    for (const child of Array.from(element.children)) {
        strip(child)
    }
}

expect.addSnapshotSerializer({
    test: (value: unknown): boolean => value instanceof Element && hasDndKitMarkup(value),

    // The clone no longer matches `test`, so printing it does not recurse.
    serialize: (value: Element, config, indentation, depth, refs, printer): string => {
        const clone = value.cloneNode(true) as Element
        strip(clone)
        return printer(clone, config, indentation, depth, refs)
    },
})

export {}
