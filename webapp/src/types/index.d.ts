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
                ListAgents(): Promise<string>
                AddAgent(entryJSON: string): Promise<string>
                UpdateAgent(entryJSON: string): Promise<string>
                RemoveAgent(name: string): Promise<void>
                ListProxies(): Promise<string>
                AddProxy(entryJSON: string): Promise<string>
                UpdateProxy(entryJSON: string): Promise<string>
                RemoveProxy(name: string): Promise<void>
                SyncAgentUsers(boardId: string): Promise<string>
                ListDeployTargets(): Promise<string>
                AddDeployTarget(entryJSON: string): Promise<string>
                UpdateDeployTarget(entryJSON: string): Promise<string>
                RemoveDeployTarget(name: string): Promise<void>
                ListFlows(boardId: string): Promise<string>
                ListFlowTriggers(): Promise<string>
                ListFlowTemplates(): Promise<string>
                ListBoardColumns(boardId: string): Promise<string>
                SaveBoardColumn(specJSON: string): Promise<string>
                RemoveBoardColumn(boardId: string, optionId: string, column: string): Promise<void>
                GetWorktreeMode(): Promise<string>
                GetCardFlow(cardId: string): Promise<string>
                GetBoardFlowOverview(boardId: string): Promise<string>
                SeedBoardAutomation(boardId: string): Promise<void>
                AddFlow(entryJSON: string): Promise<string>
                UpdateFlow(entryJSON: string): Promise<string>
                RemoveFlow(name: string): Promise<void>
                GetAgentSystemPrompt(): Promise<string>
                SetAgentSystemPrompt(text: string): Promise<void>
                GetCardSessions(cardId: string): Promise<string>
                CancelSession(cardId: string): Promise<boolean>
                StartCardSession(cardId: string, repoName: string): Promise<string>
                PromptSession(sessionId: string, text: string): Promise<void>
                AnswerPermission(sessionId: string, requestId: string, optionId: string): Promise<void>
                AnswerQuestion(sessionId: string, requestId: string, text: string): Promise<void>
                StartCardDeploy(cardId: string, branch: string): Promise<string>
                StartPlanningSession(repoName: string, agentName: string): Promise<string>
                ComposeTask(sessionId: string): Promise<string>
                AttachSession(sessionId: string): Promise<boolean>
                DetachSession(sessionId: string): Promise<void>
                CloseSession(sessionId: string): Promise<void>
            }
        }
    }

    // Wails event bus, injected alongside the bindings. Present only in the
    // desktop app; feature-detect before use.
    runtime?: {
        EventsOn(event: string, callback: (...data: any[]) => void): () => void
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
