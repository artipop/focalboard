// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import React, {type JSX} from 'react'

import CompassIcon from './compassIcon'

export default function BrokenFile(): JSX.Element {
    return (
        <CompassIcon
            icon='file-image-broken-outline'
            className='BrokenFile'
        />
    )
}
