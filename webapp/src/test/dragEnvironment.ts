// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

// jsdom has no layout engine, so every element measures 0x0, and dnd-kit -- which
// decides what you are over by comparing rectangles against a pointer -- can
// never see anything. Nor does jsdom have PointerEvent, IntersectionObserver,
// ResizeObserver, elementFromPoint, pointer capture or the Web Animations API,
// all of which dnd-kit reaches for during a drag.
//
// Supplying them makes a drag expressible in a test. Three separate regressions
// in this area went unnoticed by 900 passing tests before this existed.
//
// Opt-in, like reactFlowEnvironment.ts: faking geometry for the whole suite
// would change what unrelated tests see.

type Rect = {x: number, y: number, width: number, height: number}

const rects = new WeakMap<Element, Rect>()
const measured: Element[] = []

/** Give an element the rectangle it would have had, if anything had laid it out. */
export function setRect(element: Element, rect: Rect): void {
    if (!rects.has(element)) {
        measured.push(element)
    }
    rects.set(element, rect)
}

export function setupDragEnvironment(): void {
    const anyGlobal = global as unknown as Record<string, unknown>
    if (anyGlobal.dragEnvironmentInstalled) {
        return
    }
    anyGlobal.dragEnvironmentInstalled = true

    Element.prototype.getBoundingClientRect = function getBoundingClientRect(this: Element) {
        const r = rects.get(this) || {x: 0, y: 0, width: 0, height: 0}
        return {
            x: r.x,
            y: r.y,
            left: r.x,
            top: r.y,
            right: r.x + r.width,
            bottom: r.y + r.height,
            width: r.width,
            height: r.height,
            toJSON: () => r,
        } as DOMRect
    }

    // Both observers report once as soon as they start observing, and that first
    // report is what gives a droppable its shape -- a stub that stays silent
    // leaves every shape undefined and nothing is ever a drop target. It has to
    // be asynchronous and once, or dnd-kit goes round in circles re-observing
    // what it was just told about.
    const report = (callback: (entries: unknown[]) => void, element: Element, seen: WeakSet<Element>) => {
        if (seen.has(element)) {
            return
        }
        seen.add(element)
        queueMicrotask(() => {
            const rect = element.getBoundingClientRect()
            callback([{
                target: element,
                isIntersecting: true,
                intersectionRatio: 1,
                intersectionRect: rect,
                boundingClientRect: rect,
                rootBounds: rect,
                contentRect: rect,
                time: 0,
            }])
        })
    }

    class Observer {
        private seen = new WeakSet<Element>()
        constructor(private callback: (entries: unknown[]) => void) {}
        observe(element: Element) {
            report(this.callback, element, this.seen)
        }
        unobserve() {} // eslint-disable-line class-methods-use-this
        disconnect() {} // eslint-disable-line class-methods-use-this
        takeRecords() { // eslint-disable-line class-methods-use-this
            return []
        }
    }
    anyGlobal.IntersectionObserver = Observer
    anyGlobal.ResizeObserver = Observer

    // What is under the pointer, answered from the rectangles above; the last
    // match wins, which stands in for paint order.
    const elementFromPoint = (x: number, y: number): Element | null => {
        let found: Element | null = null
        for (const element of measured) {
            const r = rects.get(element)
            if (r && x >= r.x && x <= r.x + r.width && y >= r.y && y <= r.y + r.height) {
                found = element
            }
        }
        return found
    }

    anyGlobal.matchMedia = () => ({
        matches: false,
        addEventListener: () => {},
        removeEventListener: () => {},
    })

    Object.assign(Document.prototype, {elementFromPoint, getAnimations: () => []})
    Object.assign(Element.prototype, {
        elementFromPoint,
        getAnimations: () => [],

        // Takes a turn to finish, like a real one. An animation that completes
        // instantly hides the race this whole file exists to expose: the dragged
        // element is unmounted by the drop handler while dnd-kit is still
        // waiting for it to report itself idle.
        animate: () => ({
            cancel: () => {},
            finished: new Promise((resolve) => setTimeout(resolve, 10)),
            addEventListener: (_: string, listener: () => void) => setTimeout(listener, 10),
        }),
        setPointerCapture: () => {},
        releasePointerCapture: () => {},
        hasPointerCapture: () => false,
    })
}

// dnd-kit's sensor tests every event with `instanceof PointerEvent` before
// looking at it, so a MouseEvent carrying the right fields is silently ignored.
class TestPointerEvent extends MouseEvent {
    readonly pointerId = 1
    readonly pointerType: string
    readonly isPrimary = true

    constructor(type: string, init: MouseEventInit & {pointerType?: string}) {
        super(type, init)
        this.pointerType = init.pointerType ?? 'mouse'
    }
}

function pointerEvent(type: string, x: number, y: number): Event {
    (global as unknown as Record<string, unknown>).PointerEvent = TestPointerEvent
    return new TestPointerEvent(type, {
        bubbles: true,
        cancelable: true,
        composed: true,
        clientX: x,
        clientY: y,
        button: 0,
        buttons: type === 'pointerup' ? 0 : 1,
    })
}

type Point = {x: number, y: number}

/**
 * Drives a mouse drag the way a person would: press, move far enough to pass the
 * activation distance, move onto the target, release. Each step gets a turn of
 * the event loop, because dnd-kit measures and collides on its own schedule
 * rather than synchronously.
 */
/**
 * One press, one long move, one release -- the flick of the wrist that dnd-kit's
 * default constraints throw away, because their Delay aborts the activation when
 * the pointer travels past its tolerance before the timer fires.
 */
export async function flick(
    act: (fn: () => Promise<void>) => Promise<unknown>,
    source: Element,
    to: Point,
): Promise<void> {
    const rect = source.getBoundingClientRect()
    const from = {x: rect.left + (rect.width / 2), y: rect.top + (rect.height / 2)}
    const settle = () => new Promise((resolve) => setTimeout(resolve, 20))

    await act(async () => {
        source.dispatchEvent(pointerEvent('pointerdown', from.x, from.y))
    })
    await act(async () => {
        setRect(source, {x: to.x - (rect.width / 2), y: to.y - (rect.height / 2), width: rect.width, height: rect.height})
        document.dispatchEvent(pointerEvent('pointermove', to.x, to.y))
        await settle()
    })
    await act(async () => {
        document.dispatchEvent(pointerEvent('pointerup', to.x, to.y))
        await settle()
    })
}

export async function drag(
    act: (fn: () => Promise<void>) => Promise<unknown>,
    source: Element,
    to: Point,
): Promise<void> {
    const rect = source.getBoundingClientRect()
    const origin = {x: rect.left, y: rect.top, width: rect.width, height: rect.height}

    // Grabbed at the centre, which is both what people do and the case that
    // matters: a pointer intersection scores 1/distance-to-centre, so a card
    // held by its middle keeps its own zone permanently at distance zero.
    const from = {x: rect.left + (rect.width / 2), y: rect.top + (rect.height / 2)}
    const settle = () => new Promise((resolve) => setTimeout(resolve, 20))

    // A browser transforms the dragged element so it follows the pointer, and
    // its own drop zone follows with it. Leaving it parked at its origin makes
    // the test kinder than reality -- and it is exactly that zone, sitting under
    // the pointer for the whole drag, that once swallowed every drop.
    const follow = (x: number, y: number) => {
        setRect(source, {x: origin.x + (x - from.x), y: origin.y + (y - from.y), width: origin.width, height: origin.height})
    }

    await act(async () => {
        source.dispatchEvent(pointerEvent('pointerdown', from.x, from.y))
    })
    await act(async () => {
        follow(from.x + 20, from.y + 20)
        document.dispatchEvent(pointerEvent('pointermove', from.x + 20, from.y + 20))
        await settle()
    })
    await act(async () => {
        follow(to.x, to.y)
        document.dispatchEvent(pointerEvent('pointermove', to.x, to.y))
        await settle()
    })
    await act(async () => {
        document.dispatchEvent(pointerEvent('pointerup', to.x, to.y))
        await settle()
    })
}
