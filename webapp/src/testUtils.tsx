// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.
import {IntlProvider} from 'react-intl'
import React, {type JSX} from 'react'
import configureStore, {MockStoreEnhanced} from 'redux-mock-store'
import {thunk} from 'redux-thunk'

import {SortableProvider} from './hooks/sortable'
import {Block} from './blocks/block'

export const wrapIntl = (children?: React.ReactNode): JSX.Element => <IntlProvider locale='en'>{children}</IntlProvider>
export const wrapDNDIntl = (children?: React.ReactNode): JSX.Element => {
    return (
        <SortableProvider>
            {wrapIntl(children)}
        </SortableProvider>
    )
}

// One provider serves both halves now: the sidebar's sortables and the cards'
// drop targets live in the same dnd-kit context, so these two wrappers, which
// existed only because react-beautiful-dnd needed its own, are the same thing.
export const wrapRBDNDContext = (children?: React.ReactNode): JSX.Element => {
    return (
        <SortableProvider>
            {children}
        </SortableProvider>
    )
}

export const wrapRBDNDDroppable = (children?: React.ReactNode): JSX.Element => wrapRBDNDContext(children)

export function mockDOM(): void {
    window.focus = jest.fn()
    document.createRange = () => {
        const range = new Range()
        range.getBoundingClientRect = jest.fn()
        range.getClientRects = () => {
            return {
                item: () => null,
                length: 0,
                [Symbol.iterator]: jest.fn(),
            }
        }
        return range
    }
}
export function mockMatchMedia(result: {matches: boolean}): void {
    // We check if system preference is dark or light theme.
    // This is required to provide it's definition since
    // window.matchMedia doesn't exist in Jest.
    Object.defineProperty(window, 'matchMedia', {
        writable: true,
        value: jest.fn().mockImplementation(() => {
            return result

            // return ({
            //     matches: true,
            // })
        }),
    })
}

export function mockStateStore(middleware: MockMiddlewares, state: unknown): MockStoreEnhanced<unknown, unknown> {
    const mockStore = configureStore(middleware)
    return mockStore(state)
}

// redux-mock-store ships its own redux types, and redux 5 renamed AnyAction to
// UnknownAction, so the two disagree about Dispatch. Taking the parameter type
// from configureStore itself sidesteps the argument.
type MockMiddlewares = NonNullable<Parameters<typeof configureStore>[0]>

// redux-thunk 3 types its middleware against redux 5, redux-mock-store against
// redux 4, and the two disagree about Dispatch even though the value is the
// same function. One cast, named once, instead of one per test file.
export const mockThunk = thunk as unknown as MockMiddlewares[number]

export type BlocksById<BlockType> = {[key: string]: BlockType}

export function blocksById<BlockType extends Block>(blocks: BlockType[]): BlocksById<BlockType> {
    return blocks.reduce((res, block) => {
        res[block.id] = block
        return res
    }, {} as BlocksById<BlockType>)
}
