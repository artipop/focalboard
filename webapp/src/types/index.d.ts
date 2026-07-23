// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

type TelemetryProps = {
    trackingLocation: string
}
export interface IAppWindow extends Window {
    baseURL?: string
    frontendBaseURL?: string

    // Absolute base URL the WebSocket client should connect to, overriding the
    // page origin. Set by native desktop wrappers (e.g. the Wails macOS app)
    // whose webview origin differs from the server, so the socket reaches the
    // real server directly. Unused in browser/plugin deployments.
    webSocketBaseURL?: string
    isFocalboardPlugin?: boolean
    getCurrentTeamId?: () => string
    msCrypto: Crypto
    openInNewBrowser?: ((href: string) => void) | null

    // Bindings injected by the Wails desktop wrapper (window.go.main.App.*).
    // Absent in browser/plugin deployments; feature-detect before use.
    go?: {
        main?: {
            App?: {
                ListAgentRepos(): Promise<string>
                PickDirectory(title: string): Promise<string>
                AddAgentRepo(name: string, path: string): Promise<string>
                RemoveAgentRepo(name: string): Promise<void>
                GetCardSessions(cardId: string): Promise<string>
                CancelSession(cardId: string): Promise<boolean>
            }
        }
    }
    webkit?: {messageHandlers: {nativeApp?: {postMessage: <T>(message: T) => void}}}
    openPricingModal?: () => (telemetry: TelemetryProps) => void
}

// SuiteWindow documents all custom properties
// which may be defined on global
// window object when operating in
// the Mattermost suite environment
export type SuiteWindow = Window & {
    getCurrentTeamId?: () => string
    baseURL?: string
    frontendBaseURL?: string
    isFocalboardPlugin?: boolean
    WebappUtils?: any
}
