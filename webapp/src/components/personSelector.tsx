// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import React, {type JSX, useCallback} from 'react'
import {useIntl} from 'react-intl'

import Combobox, {type ComboboxAction} from '../widgets/combobox'
import type {ComboboxItem, ComboboxOption} from '../combobox'
import {IUser} from '../user'
import {Utils} from '../utils'
import {useAppSelector} from '../store/hooks'
import {getBoardUsers, getBoardUsersList, getMe} from '../store/users'

import {ClientConfig} from '../config/clientConfig'
import {getClientConfig} from '../store/clientConfig'
import client from '../octoClient'

import GuestBadge from '../widgets/guestBadge'
import {PropertyType} from '../properties/types'

import './personSelector.scss'

const imageURLForUser = (window as any).Components?.imageURLForUser

type Props = {
    readOnly: boolean
    userIDs: string[]
    allowAddUsers: boolean
    property?: PropertyType
    emptyDisplayValue: string
    isMulti: boolean
    closeMenuOnSelect?: boolean
    showMe?: boolean

    // On `remove` the list already excludes the person removed, so every action
    // is answered by reading the list rather than by the action's own payload.
    onChange: (items: IUser[] | IUser | null, action: ComboboxAction) => void
}

const asOption = (user: IUser): ComboboxOption<IUser> => ({id: user.id, label: user.username, data: user})

const PersonSelector = (props: Props): JSX.Element => {
    const {readOnly, userIDs, allowAddUsers, isMulti, closeMenuOnSelect = true, emptyDisplayValue, showMe = false, onChange} = props

    const clientConfig = useAppSelector<ClientConfig>(getClientConfig)
    const intl = useIntl()

    const boardUsersById = useAppSelector<{[key: string]: IUser}>(getBoardUsers)
    const boardUsers = useAppSelector<IUser[]>(getBoardUsersList)
    const boardUsersKey = Object.keys(boardUsersById) ? Utils.hashCode(JSON.stringify(Object.keys(boardUsersById))) : 0
    const me = useAppSelector<IUser|null>(getMe)

    const formatOptionLabel = (user: any): JSX.Element => {
        if (!user) {
            return <div/>
        }
        let profileImg
        if (imageURLForUser) {
            profileImg = imageURLForUser(user.id)
        }

        return (
            <div
                key={user.id}
                className={isMulti ? 'MultiPerson-item' : 'Person-item'}
            >
                {profileImg && (
                    <img
                        alt='Person-avatar'
                        src={profileImg}
                    />
                )}
                {Utils.getUserDisplayName(user, clientConfig.teammateNameDisplay)}
                <GuestBadge show={Boolean(user?.is_guest)}/>
            </div>
        )
    }

    let users: IUser[] = []
    if (Object.keys(boardUsersById).length > 0) {
        users = userIDs.map((id) => boardUsersById[id])
    }

    const loadOptions = useCallback(async (value: string): Promise<Array<ComboboxItem<IUser>>> => {
        if (!allowAddUsers) {
            const returnUsers: IUser[] = []
            if (showMe && me) {
                returnUsers.push({
                    id: me.id,
                    username: intl.formatMessage({id: 'PersonProperty.me', defaultMessage: 'Me'}),
                    email: '',
                    nickname: '',
                    firstname: '',
                    lastname: '',
                    props: {},
                    create_at: me.create_at,
                    update_at: me.update_at,
                    is_bot: false,
                    is_guest: me.is_guest,
                    roles: me.roles,
                })
                returnUsers.push(...boardUsers.filter((u) => u.id !== me.id))
            } else {
                returnUsers.push(...boardUsers)
            }
            if (value) {
                return returnUsers.filter((u) => {
                    return u.username.toLowerCase().includes(value.toLowerCase()) ||
                        u.lastname.toLowerCase().includes(value.toLowerCase()) ||
                        u.firstname.toLowerCase().includes(value.toLowerCase()) ||
                        u.nickname.toLowerCase().includes(value.toLowerCase())
                }).map(asOption)
            }
            return returnUsers.map(asOption)
        }
        const excludeBots = true
        const allUsers = await client.searchTeamUsers(value, excludeBots)
        const usersInsideBoard: IUser[] = []
        const usersOutsideBoard: IUser[] = []
        for (const u of allUsers) {
            if (boardUsersById[u.id]) {
                usersInsideBoard.push(u)
            } else {
                usersOutsideBoard.push(u)
            }
        }
        return [
            {label: intl.formatMessage({id: 'PersonProperty.board-members', defaultMessage: 'Board members'}), options: usersInsideBoard.map(asOption)},
            {label: intl.formatMessage({id: 'PersonProperty.non-board-members', defaultMessage: 'Not board members'}), options: usersOutsideBoard.map(asOption)},
        ].filter((group) => group.options.length > 0)
    }, [allowAddUsers, showMe, me, boardUsers, boardUsersById, intl])

    let primaryClass = 'Person'
    if (isMulti) {
        primaryClass = 'MultiPerson'
    }
    let secondaryClass = ''
    if (props.property) {
        secondaryClass = ` ${props.property.valueClassName(readOnly)}`
    }

    if (readOnly) {
        return (
            <div className={`${primaryClass}${secondaryClass}`}>
                {users.map((user) => formatOptionLabel(user))}
            </div>
        )
    }

    return (
        <Combobox
            key={boardUsersKey}
            loadOptions={loadOptions}
            isMulti={isMulti}
            isClearable={true}
            closeMenuOnSelect={closeMenuOnSelect}
            className={`${primaryClass}${secondaryClass}`}
            classNamePrefix={'react-select'}
            renderOption={(option) => formatOptionLabel(option.data)}
            placeholder={emptyDisplayValue}
            value={users.filter(Boolean).map(asOption)}
            onChange={(value, action) => {
                if (Array.isArray(value)) {
                    onChange(value.map((option) => option.data), action)
                } else {
                    onChange(value ? value.data : null, action)
                }
            }}
        />
    )
}

export default PersonSelector
