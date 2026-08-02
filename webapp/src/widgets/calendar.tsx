// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.
import React, {type JSX, useState} from 'react'
import {useIntl} from 'react-intl'

import {addMonths, isSameDay, isWithin, monthLabel, monthWeeks, startOfDay, startOfMonth, weekdayNames, type CalendarDay} from '../calendar'

import IconButton from './buttons/iconButton'
import CompassIcon from './icons/compassIcon'

import './calendar.scss'

type Props = {

    // A single date is a selection whose two ends are the same day, which is
    // what lets one calendar serve the date property and the date filter.
    selection?: {from?: Date, to?: Date}

    // Which month to open on; the user's own paging takes over from there.
    defaultMonth?: Date

    // 0 is Sunday. The call sites read it off the locale moment has loaded.
    firstDayOfWeek: number

    onDayClick: (day: Date) => void
    footer?: React.ReactNode
}

function dayClassName(day: CalendarDay, selection: {from?: Date, to?: Date} | undefined, today: Date): string {
    const from = selection?.from
    const to = selection?.to || selection?.from

    const names = ['Calendar__day']
    if (isSameDay(day.date, today)) {
        names.push('Calendar__day--today')
    }
    if (isWithin(day.date, from, to)) {
        names.push('Calendar__day--selected')
    }
    if (isSameDay(day.date, from)) {
        names.push('Calendar__day--start')
    }
    if (isSameDay(day.date, to)) {
        names.push('Calendar__day--end')
    }
    return names.join(' ')
}

const Calendar = (props: Props): JSX.Element => {
    const intl = useIntl()
    const [month, setMonth] = useState(() => startOfMonth(props.defaultMonth || new Date()))

    const today = new Date()
    const weeks = monthWeeks(month, props.firstDayOfWeek)
    const weekdays = weekdayNames(intl.locale, props.firstDayOfWeek)

    return (
        <div className='Calendar'>
            <div className='Calendar__nav'>
                <IconButton
                    size='small'
                    onClick={() => setMonth(addMonths(month, -1))}
                    icon={<CompassIcon icon='chevron-left'/>}
                    title={intl.formatMessage({id: 'Calendar.previousMonth', defaultMessage: 'Previous month'})}
                />
                <div className='Calendar__caption'>{monthLabel(intl.locale, month)}</div>
                <IconButton
                    size='small'
                    onClick={() => setMonth(addMonths(month, 1))}
                    icon={<CompassIcon icon='chevron-right'/>}
                    title={intl.formatMessage({id: 'Calendar.nextMonth', defaultMessage: 'Next month'})}
                />
            </div>
            <table className='Calendar__grid'>
                <thead>
                    <tr>
                        {weekdays.map((name) => (
                            <th
                                key={name}
                                scope='col'
                                className='Calendar__weekday'
                            >
                                {name}
                            </th>
                        ))}
                    </tr>
                </thead>
                <tbody>
                    {weeks.map((week) => (
                        <tr key={week[0].date.toDateString()}>
                            {week.map((day) => (
                                <td
                                    key={day.date.toDateString()}
                                    className='Calendar__cell'
                                >
                                    {day.inMonth && (
                                        <button
                                            type='button'
                                            className={dayClassName(day, props.selection, today)}

                                            // A fresh date every time: the call
                                            // sites move the clicked day to noon
                                            // in place, and the grid is reused.
                                            onClick={() => props.onDayClick(startOfDay(day.date))}
                                        >
                                            {day.date.getDate()}
                                        </button>
                                    )}
                                </td>
                            ))}
                        </tr>
                    ))}
                </tbody>
            </table>
            {props.footer && <div className='Calendar__footer'>{props.footer}</div>}
        </div>
    )
}

export default Calendar
