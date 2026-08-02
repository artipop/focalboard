// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.
import {addMonths, isSameDay, isWithin, monthLabel, monthWeeks, startOfMonth, weekdayNames} from './calendar'

const day = (year: number, month: number, date: number) => new Date(year, month - 1, date)

describe('calendar', () => {
    describe('monthWeeks', () => {
        // February 2021 began on a Monday and ended on a Sunday, so a week
        // starting on Monday covers it exactly and nothing is padded.
        it('gives whole weeks with no padding when the month fits', () => {
            const weeks = monthWeeks(day(2021, 2, 15), 1)

            expect(weeks).toHaveLength(4)
            expect(weeks.every((week) => week.length === 7)).toBe(true)
            expect(weeks.every((week) => week.every((d) => d.inMonth))).toBe(true)
            expect(weeks[0][0].date).toEqual(day(2021, 2, 1))
            expect(weeks[3][6].date).toEqual(day(2021, 2, 28))
        })

        it('pads the first and last week with the neighbouring months', () => {
            const weeks = monthWeeks(day(2021, 8, 15), 1)

            // August 2021 began on a Sunday, so a Monday-first week leads with
            // six days of July.
            expect(weeks[0].filter((d) => !d.inMonth)).toHaveLength(6)
            expect(weeks[0][0].date).toEqual(day(2021, 7, 26))
            expect(weeks[0][6].date).toEqual(day(2021, 8, 1))
        })

        it('starts the week where the locale says', () => {
            const mondayFirst = monthWeeks(day(2021, 8, 15), 1)
            const sundayFirst = monthWeeks(day(2021, 8, 15), 0)

            expect(mondayFirst[0][0].date.getDay()).toBe(1)
            expect(sundayFirst[0][0].date.getDay()).toBe(0)

            // Sunday-first, August 2021 needs no lead-in at all.
            expect(sundayFirst[0][0].date).toEqual(day(2021, 8, 1))
        })

        it('covers every day of the month exactly once', () => {
            for (let month = 1; month <= 12; month++) {
                const dates = monthWeeks(day(2024, month, 10), 1).
                    flat().
                    filter((d) => d.inMonth).
                    map((d) => d.date.getDate())

                const last = new Date(2024, month, 0).getDate()
                expect(dates).toEqual(Array.from({length: last}, (_, i) => i + 1))
            }
        })

        it('carries the grid across a year boundary', () => {
            const weeks = monthWeeks(day(2021, 12, 15), 1)
            const inMonth = weeks.flat().filter((d) => d.inMonth)

            expect(inMonth).toHaveLength(31)
            expect(weeks.flat().some((d) => d.date.getFullYear() === 2022)).toBe(true)
        })
    })

    describe('addMonths', () => {
        it('steps by whole months', () => {
            expect(addMonths(day(2021, 3, 1), 1)).toEqual(day(2021, 4, 1))
            expect(addMonths(day(2021, 1, 1), -1)).toEqual(day(2020, 12, 1))
        })

        // Stepping from a 31st is why this counts from the first of the month.
        it('does not skip a month with fewer days', () => {
            expect(addMonths(day(2021, 1, 31), 1)).toEqual(day(2021, 2, 1))
        })
    })

    describe('isWithin', () => {
        const from = day(2021, 5, 10)
        const to = day(2021, 5, 20)

        it('includes both ends', () => {
            expect(isWithin(from, from, to)).toBe(true)
            expect(isWithin(to, from, to)).toBe(true)
            expect(isWithin(day(2021, 5, 15), from, to)).toBe(true)
        })

        it('excludes what falls outside', () => {
            expect(isWithin(day(2021, 5, 9), from, to)).toBe(false)
            expect(isWithin(day(2021, 5, 21), from, to)).toBe(false)
        })

        it('is false with either end open', () => {
            expect(isWithin(from, from, undefined)).toBe(false)
            expect(isWithin(from, undefined, to)).toBe(false)
        })

        it('ignores the time of day', () => {
            const noon = new Date(2021, 4, 15, 12, 30)
            expect(isWithin(noon, from, to)).toBe(true)
        })
    })

    describe('isSameDay', () => {
        it('compares the day, not the moment', () => {
            expect(isSameDay(new Date(2021, 4, 15, 0, 0), new Date(2021, 4, 15, 23, 59))).toBe(true)
            expect(isSameDay(day(2021, 5, 15), day(2021, 5, 16))).toBe(false)
            expect(isSameDay(day(2021, 5, 15), day(2020, 5, 15))).toBe(false)
        })

        it('is false when either is missing', () => {
            expect(isSameDay(undefined, day(2021, 5, 15))).toBe(false)
            expect(isSameDay(day(2021, 5, 15), undefined)).toBe(false)
        })
    })

    describe('naming', () => {
        it('names the weekdays in the order they are drawn', () => {
            const sundayFirst = weekdayNames('en', 0)
            const mondayFirst = weekdayNames('en', 1)

            expect(sundayFirst).toHaveLength(7)
            expect(sundayFirst[0]).toMatch(/^Sun/)
            expect(mondayFirst[0]).toMatch(/^Mon/)
            expect(mondayFirst[6]).toMatch(/^Sun/)
        })

        it('names the month in the locale asked for', () => {
            expect(monthLabel('en', day(2021, 5, 15))).toBe('May 2021')
            expect(monthLabel('ru', day(2021, 5, 15))).toContain('2021')
        })
    })

    describe('startOfMonth', () => {
        it('drops the day and the time', () => {
            expect(startOfMonth(new Date(2021, 4, 15, 13, 45))).toEqual(day(2021, 5, 1))
        })
    })
})
