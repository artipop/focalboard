// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.
import React, {MutableRefObject, ReactElement, useCallback, useEffect, useMemo, useState} from 'react'
import ReactDOM from 'react-dom'

import {useLexicalComposerContext} from '@lexical/react/LexicalComposerContext'
import {
    LexicalTypeaheadMenuPlugin,
    MenuOption,
    useBasicTypeaheadTriggerMatch,
} from '@lexical/react/LexicalTypeaheadMenuPlugin'
import {$createTextNode, $getSelection, $isRangeSelection, TextNode} from 'lexical'

import debounce from 'lodash/debounce'

import {useAppSelector} from '../../../store/hooks'
import {IUser} from '../../../user'
import {getBoardUsersList, getMe} from '../../../store/users'
import {useHasPermissions} from '../../../hooks/permissions'
import {Permission} from '../../../constants'
import {BoardMember, BoardTypeOpen, MemberRole} from '../../../blocks/board'
import mutator from '../../../mutator'
import ConfirmAddUserForNotifications from '../../confirmAddUserForNotifications'
import RootPortal from '../../rootPortal'
import {getCurrentBoard} from '../../../store/boards'
import octoClient from '../../../octoClient'
import {Utils} from '../../../utils'
import {ClientConfig} from '../../../config/clientConfig'
import {getClientConfig} from '../../../store/clientConfig'

import Entry, {MentionUser} from '../entryComponent/entryComponent'

const imageURLForUser = (window as any).Components?.imageURLForUser

class MentionTypeaheadOption extends MenuOption {
    mention: MentionUser

    constructor(mention: MentionUser) {
        super(mention.name)
        this.mention = mention
    }
}

// @-mention typeahead. Reproduces the original draft-js mention flow: searches
// team/board users, inserts the picked mention as plain `@username ` text (which
// is what the plain-text editor stores), and offers to add a non-member to the
// board via ConfirmAddUserForNotifications.
type Props = {
    suppressBlurRef: MutableRefObject<boolean>
}

const MentionsPlugin = (props: Props): ReactElement => {
    const {suppressBlurRef} = props
    const [editor] = useLexicalComposerContext()

    const boardUsers = useAppSelector<IUser[]>(getBoardUsersList)
    const board = useAppSelector(getCurrentBoard)
    const clientConfig = useAppSelector<ClientConfig>(getClientConfig)
    const me = useAppSelector<IUser|null>(getMe)
    const allowManageBoardRoles = useHasPermissions(board.teamId, board.id, [Permission.ManageBoardRoles])

    const [suggestions, setSuggestions] = useState<MentionUser[]>([])
    const [confirmAddUser, setConfirmAddUser] = useState<IUser|null>(null)

    const triggerFn = useBasicTypeaheadTriggerMatch('@', {minLength: 0})

    const loadSuggestions = useCallback(async (term: string) => {
        let users: IUser[]

        if (!me?.is_guest && (allowManageBoardRoles || (board && board.type === BoardTypeOpen))) {
            const excludeBots = true
            users = await octoClient.searchTeamUsers(term, excludeBots)
        } else {
            users = boardUsers.
                filter((user) => {
                    if (!term) {
                        return true
                    }
                    return Utils.getUserDisplayName(user, clientConfig.teammateNameDisplay).includes(term)
                }).
                slice(0, 10)
        }

        const mentions: MentionUser[] = users.map(
            (user: IUser): MentionUser => ({
                name: user.username,
                avatar: `${imageURLForUser ? imageURLForUser(user.id) : ''}`,
                is_bot: user.is_bot,
                is_guest: user.is_guest,
                displayName: Utils.getUserDisplayName(user, clientConfig.teammateNameDisplay),
                isBoardMember: Boolean(boardUsers.find((u) => u.id === user.id)),
                user,
            }))
        setSuggestions(mentions)
    }, [me, allowManageBoardRoles, board, boardUsers, clientConfig])

    const debouncedLoadSuggestion = useMemo(() => debounce(loadSuggestions, 200), [loadSuggestions])

    useEffect(() => {
        // Prime with the first users (empty search returns first 10 alphabetically).
        loadSuggestions('')
    }, [])

    // Suppress the editor's blur-save while the confirm dialog holds focus.
    useEffect(() => {
        suppressBlurRef.current = Boolean(confirmAddUser)
    }, [confirmAddUser, suppressBlurRef])

    const options = useMemo(
        () => suggestions.map((mention) => new MentionTypeaheadOption(mention)),
        [suggestions],
    )

    const onQueryChange = useCallback((query: string | null) => {
        debouncedLoadSuggestion(query || '')
    }, [debouncedLoadSuggestion])

    const addUser = useCallback(async (userId: string, role: string) => {
        const newRole = role || MemberRole.Viewer
        const newMember = {
            boardId: board.id,
            userId,
            roles: role,
            schemeAdmin: newRole === MemberRole.Admin,
            schemeEditor: newRole === MemberRole.Admin || newRole === MemberRole.Editor,
            schemeCommenter: newRole === MemberRole.Admin || newRole === MemberRole.Editor || newRole === MemberRole.Commenter,
            schemeViewer: newRole === MemberRole.Admin || newRole === MemberRole.Editor || newRole === MemberRole.Commenter || newRole === MemberRole.Viewer,
        } as BoardMember

        setConfirmAddUser(null)
        editor.focus()
        await mutator.createBoardMember(newMember)
    }, [board, editor])

    const onSelectOption = useCallback((
        selectedOption: MentionTypeaheadOption,
        nodeToReplace: TextNode | null,
        closeMenu: () => void,
    ) => {
        editor.update(() => {
            const mentionText = `@${selectedOption.mention.name} `
            const newNode = $createTextNode(mentionText)
            if (nodeToReplace && nodeToReplace.isAttached()) {
                nodeToReplace.replace(newNode)
                newNode.selectNext(0, 0)
            } else {
                const selection = $getSelection()
                if ($isRangeSelection(selection)) {
                    selection.insertText(mentionText)
                }
            }
        })
        closeMenu()

        if (!selectedOption.mention.isBoardMember) {
            setConfirmAddUser(selectedOption.mention.user as IUser)
        }
    }, [editor])

    return (
        <>
            <LexicalTypeaheadMenuPlugin<MentionTypeaheadOption>
                options={options}
                onQueryChange={onQueryChange}
                onSelectOption={onSelectOption}
                triggerFn={triggerFn}
                menuRenderFn={(
                    anchorElementRef: React.RefObject<HTMLElement | null>,
                    {selectedIndex, selectOptionAndCleanUp, setHighlightedIndex}: {
                        selectedIndex: number | null
                        selectOptionAndCleanUp: (option: MentionTypeaheadOption) => void
                        setHighlightedIndex: (index: number) => void
                    },
                ) => {
                    if (!anchorElementRef.current || options.length === 0) {
                        return null
                    }
                    return ReactDOM.createPortal(
                        <div className='MarkdownEditorInput--mentions'>
                            <div role='listbox'>
                                {options.map((option, i) => (
                                    <Entry
                                        key={option.mention.name}
                                        mention={option.mention}
                                        isSelected={selectedIndex === i}
                                        onClick={() => {
                                            setHighlightedIndex(i)
                                            selectOptionAndCleanUp(option)
                                        }}
                                        onMouseEnter={() => setHighlightedIndex(i)}
                                    />
                                ))}
                            </div>
                        </div>,
                        anchorElementRef.current,
                    )
                }}
            />
            {confirmAddUser &&
                <RootPortal>
                    <ConfirmAddUserForNotifications
                        allowManageBoardRoles={allowManageBoardRoles}
                        minimumRole={board.minimumRole}
                        user={confirmAddUser}
                        onConfirm={addUser}
                        onClose={() => {
                            setConfirmAddUser(null)
                            editor.focus()
                        }}
                    />
                </RootPortal>}
        </>
    )
}

export default MentionsPlugin
