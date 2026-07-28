// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import '@testing-library/cypress/add-commands'

import 'cypress-real-events/support'

import './api_commands'
import './ui_commands'

import 'cypress-failed-log'

// Cypress >= 12 runs every test with test isolation on, resetting the app under test to
// about:blank in between. A bare `localStorage.setItem()` in a spec therefore writes to the
// spec frame's own origin, not the app's, so the app never sees it -- it would show the
// welcome page, and would not pick up the session written by cy.apiLoginUser().
// Seed the app's localStorage right before each page load instead. The spec frame stays the
// place where api_commands.ts keeps the session id between commands.
Cypress.on('window:before:load', (win) => {
    win.localStorage.setItem('welcomePageViewed', 'true')
    win.localStorage.setItem('language', 'en')

    const sessionId = localStorage.getItem('focalboardSessionId')
    if (sessionId) {
        win.localStorage.setItem('focalboardSessionId', sessionId)
    }
})
