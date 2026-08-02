// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.
import moment from 'moment'

// react-day-picker 7 shipped DateUtils and a moment adapter, and later versions
// dropped both before the library itself was dropped. The three things this
// codebase used from them, kept together so the call sites read the same.

export function isDate(value: unknown): value is Date {
    return value instanceof Date && !isNaN(value.getTime())
}

// Clicking a day inside a range moves the nearer end to it; clicking outside
// extends the range. Same behaviour DateUtils.addDayToRange had.
export function addDayToRange(day: Date, range: {from?: Date, to?: Date}): {from?: Date, to?: Date} {
    let {from, to} = range

    if (!from) {
        return {from: day, to}
    }
    if (!to) {
        return day < from ? {from: day, to: from} : {from, to: day}
    }
    if (day < from) {
        return {from: day, to}
    }
    if (day > to) {
        return {from, to: day}
    }

    // Inside the range: collapse whichever end is closer.
    const closerToFrom = day.getTime() - from.getTime() <= to.getTime() - day.getTime()
    if (closerToFrom) {
        from = day
    } else {
        to = day
    }
    return {from, to}
}

// Parses a date typed in the user's own short format ('L'), which is what
// MomentLocaleUtils.parseDate did.
export function parseLocalizedDate(value: string, locale: string): Date | undefined {
    const parsed = moment(value, 'L', locale, true)
    return parsed.isValid() ? parsed.toDate() : undefined
}
