// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import {defineConfig} from 'cypress'

import failed from 'cypress-failed-log/src/failed'

export default defineConfig({
    chromeWebSecurity: false,
    video: false,
    viewportWidth: 1600,
    viewportHeight: 1200,
    env: {
        username: 'test-user',
        password: 'test-password',
        email: 'test@mail.com',
    },
    e2e: {
        baseUrl: 'http://localhost:8088',

        // Kept in the pre-v10 order: the specs share one server and one registered
        // user, so login* has to run before the rest.
        specPattern: [
            'cypress/e2e/login*.ts',
            'cypress/e2e/create*.ts',
            'cypress/e2e/manage*.ts',
            'cypress/e2e/group*.ts',
            'cypress/e2e/card*.ts',
        ],
        setupNodeEvents(on) {
            on('task', {
                failed: failed(),
            })
        },
    },
})
