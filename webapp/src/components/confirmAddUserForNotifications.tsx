// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import React, {type JSX, useState, useRef} from 'react'
import {useIntl, FormattedMessage} from 'react-intl'

import Combobox from '../widgets/combobox'
import type {ComboboxOption} from '../combobox'

import {MemberRole} from '../blocks/board'

import {IUser} from '../user'

import ConfirmationDialog from './confirmationDialogBox'

import './confirmAddUserForNotifications.scss'

type Props = {
    user: IUser
    minimumRole: MemberRole
    allowManageBoardRoles: boolean
    onConfirm: (userId: string, role: string) => void
    onClose: () => void
}

const ConfirmAddUserForNotifications = (props: Props): JSX.Element => {
    const {user, allowManageBoardRoles} = props
    const [newUserRole, setNewUserRole] = useState<MemberRole>(props.minimumRole || MemberRole.Viewer)
    const userRole = useRef<string>(newUserRole)

    const intl = useIntl()

    // if allowed to manage board roles, only display roles higher than minimum
    const roleOptions = []
    if (allowManageBoardRoles) {
        if (props.minimumRole === MemberRole.Viewer || props.minimumRole === MemberRole.None) {
            roleOptions.push(
                {id: MemberRole.Viewer, label: intl.formatMessage({id: 'BoardMember.schemeViewer', defaultMessage: 'Viewer'})},
            )
        }
        if (props.minimumRole === MemberRole.Viewer || props.minimumRole === MemberRole.None || props.minimumRole === MemberRole.Commenter) {
            roleOptions.push(
                {id: MemberRole.Commenter, label: intl.formatMessage({id: 'BoardMember.schemeCommenter', defaultMessage: 'Commenter'})},
            )
        }
        roleOptions.push(
            {id: MemberRole.Editor, label: intl.formatMessage({id: 'BoardMember.schemeEditor', defaultMessage: 'Editor'})},
        )
        if (!user.is_guest) {
            roleOptions.push(
                {id: MemberRole.Admin, label: intl.formatMessage({id: 'BoardMember.schemeAdmin', defaultMessage: 'Admin'})},
            )
        }
    }

    // if not admin, (ie. Editor/Commentor on Public board)
    // set to minimum board role, only option, read only.
    if (!allowManageBoardRoles) {
        if (props.minimumRole === MemberRole.Viewer || props.minimumRole === MemberRole.None) {
            roleOptions.push(
                {id: MemberRole.Viewer, label: intl.formatMessage({id: 'BoardMember.schemeViewer', defaultMessage: 'Viewer'})},
            )
        }
        if (props.minimumRole === MemberRole.Commenter) {
            roleOptions.push(
                {id: MemberRole.Commenter, label: intl.formatMessage({id: 'BoardMember.schemeCommenter', defaultMessage: 'Commenter'})},
            )
        }
        if (props.minimumRole === MemberRole.Editor) {
            roleOptions.push(
                {id: MemberRole.Editor, label: intl.formatMessage({id: 'BoardMember.schemeEditor', defaultMessage: 'Editor'})},
            )
        }
    }

    const subText = (
        <div className='ConfirmAddUserForNotifications'>
            <p>
                <FormattedMessage
                    id='person.add-user-to-board-warning'
                    defaultMessage='{username} is not a member of the board, and will not receive any notifications about it.'
                    values={{username: props.user.username}}
                />
            </p>
            <p>
                <FormattedMessage
                    id='person.add-user-to-board-question'
                    defaultMessage='Do you want to add {username} to the board?'
                    values={{username: props.user.username}}
                />
            </p>
            <div className='permissions-title'>
                <label>
                    <FormattedMessage
                        id='person.add-user-to-board-permissions'
                        defaultMessage='Permissions'
                    />
                </label>
            </div>
            <Combobox
                className='select'
                classNamePrefix='select'
                portalTarget={document.body}
                isDisabled={!allowManageBoardRoles}
                isSearchable={false}
                options={roleOptions.map((o) => ({id: o.id, label: o.label, data: o}))}
                onChange={(value) => {
                    if (allowManageBoardRoles) {
                        const role = (value as ComboboxOption<{id: MemberRole}> | null)?.data.id || props.minimumRole
                        setNewUserRole(role)
                        userRole.current = role
                    }
                }}
                value={roleOptions.
                    filter((o) => o.id === newUserRole).
                    map((o) => ({id: o.id, label: o.label, data: o}))[0] || null}
            />
        </div>
    )

    return (
        <ConfirmationDialog
            dialogBox={{
                heading: intl.formatMessage({id: 'person.add-user-to-board', defaultMessage: 'Add {username} to board'}, {username: props.user.username}),
                subText,
                confirmButtonText: intl.formatMessage({id: 'person.add-user-to-board-confirm-button', defaultMessage: 'Add to board'}),
                onConfirm: () => props.onConfirm(user.id, userRole.current),
                onClose: props.onClose,
            }}
        />
    )
}

export default ConfirmAddUserForNotifications
