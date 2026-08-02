// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.
import React, {type JSX, useMemo, useState, useCallback, useEffect} from 'react'
import {useIntl} from 'react-intl'

import moment from 'moment'

import mutator from '../../mutator'

import Editable from '../../widgets/editable'
import SwitchOption from '../../widgets/menu/switchOption'
import Button from '../../widgets/buttons/button'
import Calendar from '../../widgets/calendar'

import Modal from '../../components/modal'
import ModalWrapper from '../../components/modalWrapper'
import {Utils} from '../../utils'
import useMomentLocale from '../../hooks/momentLocale'
import {addDayToRange, isDate, parseLocalizedDate} from '../../widgets/dateUtils'

import './date.scss'

import {PropertyProps} from '../types'

export type DateProperty = {
    from?: number
    to?: number
    includeTime?: boolean
    timeZone?: string
}

export function createDatePropertyFromString(initialValue: string): DateProperty {
    let dateProperty: DateProperty = {}
    if (initialValue) {
        const singleDate = new Date(Number(initialValue))
        if (singleDate && isDate(singleDate)) {
            dateProperty.from = singleDate.getTime()
        } else {
            try {
                dateProperty = JSON.parse(initialValue)
            } catch {
                //Don't do anything, return empty dateProperty
            }
        }
    }
    return dateProperty
}

function datePropertyToString(dateProperty: DateProperty): string {
    return dateProperty.from || dateProperty.to ? JSON.stringify(dateProperty) : ''
}

function DateRange(props: PropertyProps): JSX.Element {
    const {propertyValue, propertyTemplate, showEmptyPlaceholder, readOnly, board, card} = props
    const [value, setValue] = useState(propertyValue)
    const intl = useIntl()

    useEffect(() => {
        if (value !== propertyValue) {
            setValue(propertyValue)
        }
    }, [propertyValue, setValue])

    const onChange = useCallback((newValue: string) => {
        if (value !== newValue) {
            setValue(newValue)
        }
    }, [value, board.id, card, propertyTemplate.id])

    const getDisplayDate = (date: Date | null | undefined) => {
        let displayDate = ''
        if (date) {
            displayDate = Utils.displayDate(date, intl)
        }
        return displayDate
    }

    const timeZoneOffset = (date: number): number => {
        return new Date(date).getTimezoneOffset() * 60 * 1000
    }

    const dateProperty = useMemo(() => createDatePropertyFromString(value as string), [value])
    const [showDialog, setShowDialog] = useState(false)

    // Keep dateProperty as UTC,
    // dateFrom / dateTo will need converted to local time, to ensure date stays consistent
    // dateFrom / dateTo will be used for input and calendar dates
    const dateFrom = dateProperty.from ? new Date(dateProperty.from + (dateProperty.includeTime ? 0 : timeZoneOffset(dateProperty.from))) : undefined
    const dateTo = dateProperty.to ? new Date(dateProperty.to + (dateProperty.includeTime ? 0 : timeZoneOffset(dateProperty.to))) : undefined
    const [fromInput, setFromInput] = useState<string>(getDisplayDate(dateFrom))
    const [toInput, setToInput] = useState<string>(getDisplayDate(dateTo))

    const isRange = dateTo !== undefined

    const locale = intl.locale.toLowerCase()
    useMomentLocale(locale)
    const firstDayOfWeek = moment.localeData(locale).firstDayOfWeek()

    const handleDayClick = (day: Date) => {
        const range: DateProperty = {}
        day.setHours(12)
        if (isRange) {
            const newRange = addDayToRange(day, {from: dateFrom, to: dateTo})
            range.from = newRange.from?.getTime()
            range.to = newRange.to?.getTime()
        } else {
            range.from = day.getTime()
            range.to = undefined
        }
        saveRangeValue(range)
    }

    const onRangeClick = () => {
        let range: DateProperty = {
            from: dateFrom?.getTime(),
            to: dateFrom?.getTime(),
        }
        if (isRange) {
            range = ({
                from: dateFrom?.getTime(),
                to: undefined,
            })
        }
        saveRangeValue(range)
    }

    const onClear = () => {
        saveRangeValue({})
    }

    const saveRangeValue = (range: DateProperty) => {
        const rangeUTC = {...range}
        if (rangeUTC.from) {
            rangeUTC.from -= dateProperty.includeTime ? 0 : timeZoneOffset(rangeUTC.from)
        }
        if (rangeUTC.to) {
            rangeUTC.to -= dateProperty.includeTime ? 0 : timeZoneOffset(rangeUTC.to)
        }

        onChange(datePropertyToString(rangeUTC))
        setFromInput(getDisplayDate(range.from ? new Date(range.from) : undefined))
        setToInput(getDisplayDate(range.to ? new Date(range.to) : undefined))
    }

    let displayValue = ''
    if (dateFrom) {
        displayValue = getDisplayDate(dateFrom)
    }
    if (dateTo) {
        displayValue += ' → ' + getDisplayDate(dateTo)
    }

    const onClose = () => {
        const newDate = datePropertyToString(dateProperty)
        onChange(newDate)
        mutator.changePropertyValue(board.id, card, propertyTemplate.id, newDate)
        setShowDialog(false)
    }

    let buttonText = displayValue
    if (!buttonText && showEmptyPlaceholder) {
        buttonText = intl.formatMessage({id: 'DateRange.empty', defaultMessage: 'Empty'})
    }

    const className = props.property.valueClassName(readOnly)
    if (readOnly) {
        return <div className={className}>{displayValue}</div>
    }

    return (
        <div className={`DateRange ${displayValue ? '' : 'empty'} ` + className}>
            <Button
                onClick={() => setShowDialog(true)}
            >
                {buttonText}
            </Button>

            {showDialog &&
            <ModalWrapper>
                <Modal
                    onClose={() => onClose()}
                >
                    <div
                        className={className + '-overlayWrapper'}
                    >
                        <div className={className + '-overlay'}>
                            <div className={'inputContainer'}>
                                <Editable
                                    value={fromInput}
                                    placeholderText={moment.localeData(locale).longDateFormat('L')}
                                    onFocus={() => {
                                        if (dateFrom) {
                                            return setFromInput(Utils.inputDate(dateFrom, intl))
                                        }
                                        return undefined
                                    }}
                                    onChange={setFromInput}
                                    onSave={() => {
                                        const newDate = parseLocalizedDate(fromInput, intl.locale)
                                        if (newDate && isDate(newDate)) {
                                            newDate.setHours(12)
                                            const range: DateProperty = {
                                                from: newDate.getTime(),
                                                to: dateTo?.getTime(),
                                            }
                                            saveRangeValue(range)
                                        } else {
                                            setFromInput(getDisplayDate(dateFrom))
                                        }
                                    }}
                                    onCancel={() => {
                                        setFromInput(getDisplayDate(dateFrom))
                                    }}
                                />
                                {dateTo &&
                                    <Editable
                                        value={toInput}
                                        placeholderText={moment.localeData(locale).longDateFormat('L')}
                                        onFocus={() => {
                                            if (dateTo) {
                                                return setToInput(Utils.inputDate(dateTo, intl))
                                            }
                                            return undefined
                                        }}
                                        onChange={setToInput}
                                        onSave={() => {
                                            const newDate = parseLocalizedDate(toInput, intl.locale)
                                            if (newDate && isDate(newDate)) {
                                                newDate.setHours(12)
                                                const range: DateProperty = {
                                                    from: dateFrom?.getTime(),
                                                    to: newDate.getTime(),
                                                }
                                                saveRangeValue(range)
                                            } else {
                                                setToInput(getDisplayDate(dateTo))
                                            }
                                        }}
                                        onCancel={() => {
                                            setToInput(getDisplayDate(dateTo))
                                        }}
                                    />
                                }
                            </div>
                            <Calendar
                                onDayClick={handleDayClick}
                                defaultMonth={dateFrom || new Date()}
                                firstDayOfWeek={firstDayOfWeek}
                                selection={{from: dateFrom, to: dateTo}}
                                footer={
                                    <Button
                                        onClick={() => {
                                            const today = new Date()
                                            today.setHours(0, 0, 0, 0)
                                            handleDayClick(today)
                                        }}
                                    >
                                        {intl.formatMessage({id: 'DateRange.today', defaultMessage: 'Today'})}
                                    </Button>
                                }
                            />
                            <hr/>
                            <SwitchOption
                                key={'EndDateOn'}
                                id={'EndDateOn'}
                                name={intl.formatMessage({id: 'DateRange.endDate', defaultMessage: 'End date'})}
                                isOn={isRange}
                                onClick={onRangeClick}
                            />
                            <hr/>
                            <div
                                className='MenuOption menu-option'
                            >
                                <Button
                                    onClick={onClear}
                                >
                                    {intl.formatMessage({id: 'DateRange.clear', defaultMessage: 'Clear'})}
                                </Button>
                            </div>
                        </div>
                    </div>
                </Modal>
            </ModalWrapper>
            }
        </div>
    )
}

export default DateRange
