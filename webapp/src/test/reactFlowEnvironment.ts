// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

// React Flow measures the page: it observes the container's size and reads a
// transform matrix off it. jsdom has neither ResizeObserver nor DOMMatrix, and
// every element it lays out is 0×0, so without these the canvas throws instead
// of rendering. Kept here rather than in a global jest setup because faking
// element sizes for the whole suite would change what other tests see.
export function setupReactFlowEnvironment(): void {
    const anyGlobal = global as any

    if (!anyGlobal.ResizeObserver) {
        anyGlobal.ResizeObserver = class {
            observe() {} // eslint-disable-line class-methods-use-this
            unobserve() {} // eslint-disable-line class-methods-use-this
            disconnect() {} // eslint-disable-line class-methods-use-this
        }
    }

    if (!anyGlobal.DOMMatrixReadOnly) {
        anyGlobal.DOMMatrixReadOnly = class {
            m22: number
            constructor(transform: string) {
                const scale = transform?.match(/scale\(([1-9.]+)\)/)?.[1]
                this.m22 = scale === undefined ? 1 : Number(scale)
            }
        }
    }

    // A node with no size is a node React Flow refuses to place. Redefining a
    // prototype property twice throws, so this half runs once per process.
    if (!anyGlobal.reactFlowSizedElements) {
        anyGlobal.reactFlowSizedElements = true
        Object.defineProperties(global.HTMLElement.prototype, {
            offsetHeight: {get() {
                return parseFloat(this.style.height) || 1
            }},
            offsetWidth: {get() {
                return parseFloat(this.style.width) || 1
            }},
        })
        anyGlobal.SVGElement.prototype.getBBox = () => ({x: 0, y: 0, width: 0, height: 0})
    }
}
