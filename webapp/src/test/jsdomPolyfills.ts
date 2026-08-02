// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

// jsdom implements no layout, so it has never had scrollIntoView. Under jsdom 16
// that went unnoticed; jsdom 26 delivers the focus event that rootInput's onFocus
// handler hangs off, and the call started throwing. Scrolling is exactly the kind
// of thing a headless DOM has nothing to say about, so a no-op is the whole fix --
// unlike the sizing fakes in reactFlowEnvironment.ts, it changes nothing a test
// can observe, which is why it belongs in a global setup file.
if (!global.Element.prototype.scrollIntoView) {
    global.Element.prototype.scrollIntoView = () => {}
}

// A side-effect-only file is a global script under --isolatedModules; this makes
// it a module without changing what it does.
export {}
