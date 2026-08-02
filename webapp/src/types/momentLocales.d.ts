// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

// moment ships this bundle without types, and it is imported purely for the
// side effect of registering every locale. Under the old `node` module
// resolution TypeScript let it pass untyped; `bundler` asks for a declaration.
declare module 'moment/min/locales'
