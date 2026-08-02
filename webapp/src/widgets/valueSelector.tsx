// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.
import React, {type JSX} from 'react'
import {useIntl} from 'react-intl'

import {IPropertyOption} from '../blocks/board'
import {Constants} from '../constants'
import type {ComboboxOption} from '../combobox'

import Combobox, {type ComboboxContext} from './combobox'
import Menu from './menu'
import MenuWrapper from './menuWrapper'
import IconButton from './buttons/iconButton'
import OptionsIcon from './icons/options'
import DeleteIcon from './icons/delete'
import CloseIcon from './icons/close'
import Label from './label'

import './valueSelector.scss'

type Props = {
    options: IPropertyOption[]
    value?: IPropertyOption | IPropertyOption[]
    emptyValue: string
    onCreate: (value: string) => void
    onChange: (value: string | string[]) => void
    onChangeColor: (option: IPropertyOption, color: string) => void
    onDeleteOption: (option: IPropertyOption) => void
    isMulti?: boolean
    onDeleteValue?: (value: IPropertyOption) => void
    onBlur?: () => void
}

type LabelProps = {
    option: IPropertyOption
    context: ComboboxContext
    onChangeColor: (option: IPropertyOption, color: string) => void
    onDeleteOption: (option: IPropertyOption) => void
    onDeleteValue?: (value: IPropertyOption) => void
    isMulti?: boolean
}

const ValueSelectorLabel = (props: LabelProps): JSX.Element => {
    const {option, onDeleteValue, context, isMulti} = props
    const intl = useIntl()
    if (context === 'value') {
        let className = onDeleteValue ? 'Label-no-padding' : 'Label-single-select'
        if (!isMulti) {
            className += ' Label-no-margin'
        }
        return (
            <Label
                color={option.color}
                className={className}
            >
                <span className='Label-text'>{option.value}</span>
                {onDeleteValue &&
                    <IconButton
                        onClick={() => onDeleteValue(option)}
                        icon={<CloseIcon/>}
                        title='Clear'
                        className='margin-left delete-value'
                    />
                }
            </Label>
        )
    }
    return (
        <div
            className='value-menu-option'
            role='menuitem'
        >
            <div className='label-container'>
                <Label color={option.color}>{option.value}</Label>
            </div>
            <MenuWrapper stopPropagationOnToggle={true}>
                <IconButton
                    title={intl.formatMessage({id: 'ValueSelectorLabel.openMenu', defaultMessage: 'Open menu'})}
                    icon={<OptionsIcon/>}
                />
                <Menu position='left'>
                    <Menu.Text
                        id='delete'
                        icon={<DeleteIcon/>}
                        name={intl.formatMessage({id: 'BoardComponent.delete', defaultMessage: 'Delete'})}
                        onClick={() => props.onDeleteOption(option)}
                    />
                    <Menu.Separator/>
                    {Object.entries(Constants.menuColors).map(([key, color]: [string, string]) => (
                        <Menu.Color
                            key={key}
                            id={key}
                            name={color}
                            onClick={() => props.onChangeColor(option, key)}
                        />
                    ))}
                </Menu>
            </MenuWrapper>
        </div>
    )
}

function ValueSelector(props: Props): JSX.Element {
    const intl = useIntl()

    const asOption = (option: IPropertyOption): ComboboxOption<IPropertyOption> => ({
        id: option.id,
        label: option.value,
        data: option,
    })

    let chosen: IPropertyOption[] = []
    if (props.value) {
        chosen = Array.isArray(props.value) ? props.value : [props.value]
    }

    return (
        <Combobox
            className='ValueSelector'
            classNamePrefix='ValueSelector'
            ariaLabel={intl.formatMessage({id: 'ValueSelector.valueSelector', defaultMessage: 'Value selector'})}
            noOptionsMessage={intl.formatMessage({id: 'ValueSelector.noOptions', defaultMessage: 'No options. Start typing to add the first one!'})}
            isMulti={props.isMulti}
            isClearable={true}
            autoFocus={true}

            // A multi-select keeps its list open between choices; a single one
            // opens on the focus it takes and closes on the choice.
            menuIsOpen={props.isMulti ? true : undefined}
            options={props.options.map(asOption)}
            value={chosen.map(asOption)}
            valuesOwnTheirRemove={true}
            renderOption={(option, context) => (
                <ValueSelectorLabel
                    option={option.data}
                    context={context}
                    isMulti={props.isMulti}
                    onChangeColor={props.onChangeColor}
                    onDeleteOption={props.onDeleteOption}
                    onDeleteValue={props.onDeleteValue}
                />
            )}
            onCreate={props.onCreate}
            onKeyDown={(event) => {
                if (event.key === 'Escape') {
                    props.onBlur?.()
                }
            }}
            onBlur={props.onBlur}
            onChange={(value, action) => {
                if (action === 'clear') {
                    props.onChange('')
                    return
                }
                if (Array.isArray(value)) {
                    props.onChange(value.map((option) => option.id))
                } else if (value) {
                    props.onChange(value.id)
                    props.onBlur?.()
                }
            }}
        />
    )
}

export default React.memo(ValueSelector)
