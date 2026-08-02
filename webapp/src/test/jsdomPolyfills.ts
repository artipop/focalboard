// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

// jsdom's global has no fetch, and emoji-mart calls it while its module is
// being evaluated. isomorphic-fetch is already a dependency for exactly this;
// installing it once here saves the individual imports scattered through the
// suites from having to run first.
import 'isomorphic-fetch'

// jsdom implements no layout, so it has never had scrollIntoView. Under jsdom 16
// that went unnoticed; jsdom 26 delivers the focus event that rootInput's onFocus
// handler hangs off, and the call started throwing. Scrolling is exactly the kind
// of thing a headless DOM has nothing to say about, so a no-op is the whole fix --
// unlike the sizing fakes in reactFlowEnvironment.ts, it changes nothing a test
// can observe, which is why it belongs in a global setup file.
if (!global.Element.prototype.scrollIntoView) {
    global.Element.prototype.scrollIntoView = () => {}
}

// @dnd-kit/dom reads ResizeObserver while the module is being evaluated, and
// testUtils pulls it into nearly every suite, so a stub has to exist before any
// import runs. reactFlowEnvironment.ts installs one too, but that is opt-in
// because it also fakes element sizes -- this one only stops the ReferenceError.
// jsdom implements no PointerEvent at all, and @dnd-kit/dom's pointer sensor
// tests every event with `event instanceof PointerEvent` -- against undefined
// that is a TypeError, not a false. A constructor is all the guard needs: the
// mouse events tests dispatch are correctly not instances of it.
const anyWindow = global as unknown as {PointerEvent?: unknown}
if (!anyWindow.PointerEvent) {
    anyWindow.PointerEvent = class PointerEvent extends MouseEvent {}
}

const anyGlobal = global as unknown as {ResizeObserver?: unknown}
if (!anyGlobal.ResizeObserver) {
    anyGlobal.ResizeObserver = class {
        observe() {} // eslint-disable-line class-methods-use-this
        unobserve() {} // eslint-disable-line class-methods-use-this
        disconnect() {} // eslint-disable-line class-methods-use-this
    }
}

// A side-effect-only file is a global script under --isolatedModules; this makes
// it a module without changing what it does.
export {}
