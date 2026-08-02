// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

// The month grid behind the two date pickers, with no framework in it.
// `widgets/calendar.tsx` is the React view, and a port to another framework
// rewrites that and nothing here.
//
// This replaces react-day-picker, and with it date-fns, which was only ever
// pulled in to name the days: `Intl.DateTimeFormat` does that in every browser
// we target, and it already knows every locale the UI is translated into.

const DAYS_IN_WEEK = 7

export type CalendarDay = {
    date: Date

    // The first and last week of a grid are padded with days of the
    // neighbouring months; both pickers draw those as blanks.
    inMonth: boolean
}

export function startOfDay(date: Date): Date {
    const start = new Date(date)
    start.setHours(0, 0, 0, 0)
    return start
}

export function startOfMonth(date: Date): Date {
    return new Date(date.getFullYear(), date.getMonth(), 1)
}

export function isSameDay(a: Date | undefined, b: Date | undefined): boolean {
    return Boolean(a && b &&
        a.getFullYear() === b.getFullYear() &&
        a.getMonth() === b.getMonth() &&
        a.getDate() === b.getDate())
}

// Counting from the first of the month, so stepping away from a 31st never
// lands in the month after the one asked for.
export function addMonths(month: Date, delta: number): Date {
    return new Date(month.getFullYear(), month.getMonth() + delta, 1)
}

// Whether `day` falls in [from, to], with either end open.
export function isWithin(day: Date, from: Date | undefined, to: Date | undefined): boolean {
    if (!from || !to) {
        return false
    }

    const at = startOfDay(day).getTime()
    return at >= startOfDay(from).getTime() && at <= startOfDay(to).getTime()
}

// The weeks of the month `month` falls in, each one `DAYS_IN_WEEK` long and
// starting on `firstDayOfWeek` — 0 is Sunday, the way `Date.getDay` counts.
export function monthWeeks(month: Date, firstDayOfWeek: number): CalendarDay[][] {
    const first = startOfMonth(month)
    const lead = ((first.getDay() - firstDayOfWeek) + DAYS_IN_WEEK) % DAYS_IN_WEEK

    const cursor = new Date(first)
    cursor.setDate(1 - lead)

    const weeks: CalendarDay[][] = []
    do {
        const week: CalendarDay[] = []
        for (let i = 0; i < DAYS_IN_WEEK; i++) {
            week.push({
                date: new Date(cursor),
                inMonth: cursor.getMonth() === first.getMonth(),
            })
            cursor.setDate(cursor.getDate() + 1)
        }
        weeks.push(week)
    } while (cursor.getMonth() === first.getMonth())

    return weeks
}

// August 2021 began on a Sunday, so it is a week to read day names off.
const KNOWN_SUNDAY = new Date(2021, 7, 1)

export function weekdayNames(locale: string, firstDayOfWeek: number): string[] {
    const format = new Intl.DateTimeFormat(locale, {weekday: 'short'})

    const names: string[] = []
    for (let i = 0; i < DAYS_IN_WEEK; i++) {
        const day = new Date(KNOWN_SUNDAY)
        day.setDate(day.getDate() + ((firstDayOfWeek + i) % DAYS_IN_WEEK))
        names.push(format.format(day))
    }
    return names
}

export function monthLabel(locale: string, month: Date): string {
    return new Intl.DateTimeFormat(locale, {month: 'long', year: 'numeric'}).format(month)
}
