// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import React, {type JSX, useState, useEffect} from 'react'
import {FormattedMessage, IntlShape} from 'react-intl'

import {Constants, Permission} from '../../constants'
import {IPropertyOption, IPropertyTemplate, Board, BoardGroup} from '../../blocks/board'
import {BoardView} from '../../blocks/boardView'
import {Card} from '../../blocks/card'
import mutator from '../../mutator'
import IconButton from '../../widgets/buttons/iconButton'
import AddIcon from '../../widgets/icons/add'
import DeleteIcon from '../../widgets/icons/delete'
import HideIcon from '../../widgets/icons/hide'
import OptionsIcon from '../../widgets/icons/options'
import Menu from '../../widgets/menu'
import MenuWrapper from '../../widgets/menuWrapper'
import Editable from '../../widgets/editable'
import Label from '../../widgets/label'
import {useHasCurrentBoardPermissions} from '../../hooks/permissions'
import {useSortable} from '../../hooks/sortable'

import BoardPermissionGate from '../permissions/boardPermissionGate'

import ColumnBadge, {invalidateBoardColumns} from '../acp/columnBadge'
import ColumnSettingsDialog, {isColumnSettingsAvailable} from '../acp/columnSettingsDialog'

import {KanbanCalculation} from './calculation/calculation'

type Props = {
    board: Board
    activeView: BoardView
    group: BoardGroup
    groupByProperty?: IPropertyTemplate
    intl: IntlShape
    readonly: boolean
    addCard: (groupByOptionId?: string, show?: boolean) => Promise<void>
    propertyNameChanged: (option: IPropertyOption, text: string) => Promise<void>
    onDropToColumn: (srcOption: IPropertyOption, card?: Card, dstOption?: IPropertyOption) => void
    calculationMenuOpen: boolean
    onCalculationMenuOpen: () => void
    onCalculationMenuClose: () => void
}

const defaultCalculation = 'count'
const defaultProperty: IPropertyTemplate = {
    id: Constants.titleColumnId,
} as IPropertyTemplate

export default function KanbanColumnHeader(props: Props): JSX.Element {
    const {board, activeView, intl, group, groupByProperty} = props
    const [groupTitle, setGroupTitle] = useState(group.option.value)
    const canEditBoardProperties = useHasCurrentBoardPermissions([Permission.ManageBoardProperties])
    const canEditOption = groupByProperty?.type !== 'person' && group.option.id

    const [showColumnSettings, setShowColumnSettings] = useState(false)

    const [isDragging, isOver, headerRef] = useSortable(
        'column',
        group.option,
        Boolean(canEditBoardProperties),
        (src: IPropertyOption) => props.onDropToColumn(src, undefined, group.option),
    )

    useEffect(() => {
        setGroupTitle(group.option.value)
    }, [group.option.value])

    let className = 'octo-board-header-cell KanbanColumnHeader'
    if (isOver) {
        className += ' dragover'
    }

    const groupCalculation = props.activeView.fields.kanbanCalculations[props.group.option.id]
    const calculationValue = groupCalculation ? groupCalculation.calculation : defaultCalculation
    const calculationProperty = groupCalculation ? props.board.cardProperties.find((property) => property.id === groupCalculation.propertyId) || defaultProperty : defaultProperty
    return (
        <div
            key={group.option.id || 'empty'}
            ref={headerRef}
            style={{opacity: isDragging ? 0.5 : 1}}
            className={className}
        >
            {!group.option.id &&
                <Label
                    title={intl.formatMessage({
                        id: 'BoardComponent.no-property-title',
                        defaultMessage: 'Items with an empty {property} property will go here. This column cannot be removed.',
                    }, {property: groupByProperty!.name})}
                >
                    <FormattedMessage
                        id='BoardComponent.no-property'
                        defaultMessage='No {property}'
                        values={{
                            property: groupByProperty!.name,
                        }}
                    />
                </Label>}
            {groupByProperty?.type === 'person' &&
                <Label>
                    {groupTitle}
                </Label>}
            {canEditOption &&
                <Label color={group.option.color}>
                    <Editable
                        value={groupTitle}
                        placeholderText='New Select'
                        onChange={setGroupTitle}
                        onSave={() => {
                            if (groupTitle.trim() === '') {
                                setGroupTitle(group.option.value)
                            }
                            props.propertyNameChanged(group.option, groupTitle)
                        }}
                        onCancel={() => {
                            setGroupTitle(group.option.value)
                        }}
                        readonly={props.readonly || !canEditBoardProperties}
                        spellCheck={true}
                    />
                </Label>}
            <KanbanCalculation
                cards={group.cards}
                menuOpen={props.calculationMenuOpen}
                value={calculationValue}
                property={calculationProperty}
                onMenuClose={props.onCalculationMenuClose}
                onMenuOpen={props.onCalculationMenuOpen}
                cardProperties={board.cardProperties}
                readonly={props.readonly || !canEditBoardProperties}
                onChange={(data: {calculation: string, propertyId: string}) => {
                    if (data.calculation === calculationValue && data.propertyId === calculationProperty.id) {
                        return
                    }

                    const newCalculations = {
                        ...props.activeView.fields.kanbanCalculations,
                    }
                    newCalculations[props.group.option.id] = {
                        calculation: data.calculation,
                        propertyId: data.propertyId,
                    }

                    mutator.changeViewKanbanCalculations(board.id, props.activeView.id, props.activeView.fields.kanbanCalculations, newCalculations)
                }}
            />
            {Boolean(group.option.id) && groupByProperty &&
                <ColumnBadge
                    boardId={board.id}
                    optionId={group.option.id}
                    columnName={group.option.value}
                />}
            <div className='octo-spacer'/>
            {!props.readonly &&
                <>
                    <BoardPermissionGate permissions={[Permission.ManageBoardProperties]}>
                        <MenuWrapper>
                            <IconButton icon={<OptionsIcon/>}/>
                            <Menu>
                                <Menu.Text
                                    id='hide'
                                    icon={<HideIcon/>}
                                    name={intl.formatMessage({id: 'BoardComponent.hide', defaultMessage: 'Hide'})}
                                    onClick={() => mutator.hideViewColumn(board.id, activeView, group.option.id || '')}
                                />
                                {/* An empty array (unlike false/null) leaves no wrapper
                                    div behind: Menu wraps every child slot in a div. */}
                                {canEditOption && isColumnSettingsAvailable() ? [
                                    <Menu.Text
                                        key='columnAgents'
                                        id='columnAgents'
                                        name={intl.formatMessage({id: 'BoardComponent.column-agents', defaultMessage: 'Agents in this column…'})}
                                        onClick={() => setShowColumnSettings(true)}
                                    />,
                                ] : []}
                                {canEditOption &&
                                    <>
                                        <Menu.Text
                                            id='delete'
                                            icon={<DeleteIcon/>}
                                            name={intl.formatMessage({id: 'BoardComponent.delete', defaultMessage: 'Delete'})}
                                            onClick={() => mutator.deletePropertyOption(board.id, board.cardProperties, groupByProperty!, group.option)}
                                        />
                                        <Menu.Separator/>
                                        {Object.entries(Constants.menuColors).map(([key, color]) => (
                                            <Menu.Color
                                                key={key}
                                                id={key}
                                                name={color}
                                                onClick={() => mutator.changePropertyOptionColor(board.id, board.cardProperties, groupByProperty!, group.option, key)}
                                            />
                                        ))}
                                    </>}
                            </Menu>
                        </MenuWrapper>
                    </BoardPermissionGate>
                    <BoardPermissionGate permissions={[Permission.ManageBoardCards]}>
                        <IconButton
                            icon={<AddIcon/>}
                            onClick={() => {
                                props.addCard(group.option.id, true)
                            }}
                        />
                    </BoardPermissionGate>
                </>
            }
            {showColumnSettings && groupByProperty &&
                <ColumnSettingsDialog
                    boardId={board.id}
                    property={groupByProperty}
                    option={group.option}
                    onClose={() => setShowColumnSettings(false)}
                    onSaved={invalidateBoardColumns}
                />}
        </div>
    )
}
