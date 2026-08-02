// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.
import React from 'react'
import {createRoot} from 'react-dom/client'
import {Provider as ReduxProvider} from 'react-redux'
import {init as initEmojiMart} from 'emoji-mart'
import emojiMartData from '@emoji-mart/data'

import App from './app'
import {initThemes} from './theme'
import {importNativeAppSettings} from './nativeApp'

import {IUser} from './user'
import {getMe} from './store/users'
import {useAppSelector} from './store/hooks'

import '@mattermost/compass-icons/css/compass-icons.css'

import './styles/variables.scss'
import './styles/main.scss'
import './styles/labels.scss'
import './styles/_markdown.scss'

import store from './store'
import WithWebSockets from './components/withWebSockets'

// emoji-mart 5 persists skin/frequently-used itself, under the same `emoji-mart.*`
// localStorage keys UserSettings used to route for it, so it needs no handlers --
// only its data set, which v5 no longer bundles.
initEmojiMart({data: emojiMartData})
importNativeAppSettings()

initThemes()

const MainApp = () => {
    const me = useAppSelector<IUser|null>(getMe)

    return (
        <WithWebSockets userId={me?.id}>
            <App/>
        </WithWebSockets>
    )
}

createRoot(document.getElementById('focalboard-app')!).render(
    <ReduxProvider store={store}>
        <MainApp/>
    </ReduxProvider>,
)
