// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

// Jest 27's bundled resolver does not understand the package `exports` field, so
// it cannot resolve the subpath entry points that Lexical ships (e.g.
// `@lexical/react/LexicalComposer`). Delegate those to Node's own resolver, which
// honours `exports` (and picks the CommonJS `require` condition), and fall back to
// Jest's default resolver for everything else.
module.exports = (request, options) => {
    if (request === 'lexical' || request.startsWith('@lexical/')) {
        try {
            return require.resolve(request, {paths: [options.basedir]})
        } catch (e) {
            // Fall through to the default resolver below.
        }
    }
    return options.defaultResolver(request, options)
}
