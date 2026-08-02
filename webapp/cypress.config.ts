// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import fs from 'fs'
import path from 'path'

import {defineConfig} from 'cypress'

import failed from 'cypress-failed-log/src/failed'

export default defineConfig({
    chromeWebSecurity: false,
    video: false,

    // Cypress' own asset cleanup shells out to the `trash` package, whose macOS helper
    // (trash/lib/macos-trash) is still an x86_64 binary inside the otherwise-arm64 app.
    // On Apple Silicon without Rosetta that spawn fails with `Unknown system error -86`
    // (EBADARCH): harmless for results, but it prints a warning *and* leaves stale
    // screenshots piling up as "(failed) (1).png", "(2).png"... Do the cleanup in-process
    // instead -- no child process, same outcome on every platform.
    trashAssetsBeforeRuns: false,
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
            'cypress/e2e/kanban*.ts',
        ],
        setupNodeEvents(on, config) {
            on('before:run', () => {
                for (const dir of [config.screenshotsFolder, config.videosFolder]) {
                    if (dir) {
                        fs.rmSync(path.resolve(dir), {recursive: true, force: true})
                    }
                }
            })

            on('task', {
                failed: failed(),
            })
        },
    },
})
