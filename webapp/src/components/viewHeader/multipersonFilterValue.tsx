// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import React, {type JSX} from 'react'
import {useIntl} from 'react-intl'

import {Utils} from '../../utils'
import mutator from '../../mutator'
import {BoardView} from '../../blocks/boardView'

import {FilterClause} from '../../blocks/filterClause'
import {createFilterGroup} from '../../blocks/filterGroup'

import PersonSelector from '../personSelector'

import './multiperson.scss'

type Props = {
    view: BoardView
    filter: FilterClause
}

const MultiPersonFilterValue = (props: Props): JSX.Element => {
    const {filter, view} = props
    const intl = useIntl()
    const emptyDisplayValue = intl.formatMessage({id: 'ConfirmPerson.search', defaultMessage: 'Search...'})

    return (
        <PersonSelector
            userIDs={filter.values}
            allowAddUsers={false}
            isMulti={true}
            readOnly={false}
            emptyDisplayValue={emptyDisplayValue}
            showMe={true}
            closeMenuOnSelect={false}
            onChange={(items) => {
                const filterIndex = view.fields.filter.filters.indexOf(filter)
                Utils.assert(filterIndex >= 0, "Can't find filter")

                const filterGroup = createFilterGroup(view.fields.filter)
                const newFilter = filterGroup.filters[filterIndex] as FilterClause
                Utils.assert(newFilter, `No filter at index ${filterIndex}`)

                // Selecting, removing and clearing all arrive as the list that
                // is left, so there is nothing to tell apart.
                newFilter.values = Array.isArray(items) ? items.map((user) => user.id) : []
                mutator.changeViewFilter(view.boardId, view.id, view.fields.filter, filterGroup)
            }}
        />
    )
}

export default MultiPersonFilterValue
