// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import React, {type JSX} from 'react'

import CompassIcon from './compassIcon'

export default function CreateNewFolder(): JSX.Element {
    return (
        <CompassIcon
            icon='folder-plus-outline'
            className='CreateNewFolderIcon'
        />
    )
}
