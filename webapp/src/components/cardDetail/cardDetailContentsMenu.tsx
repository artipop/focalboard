// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.
import React, {type JSX, useCallback} from 'react'
import {FormattedMessage, IntlShape, useIntl} from 'react-intl'

import {BlockTypes} from '../../blocks/block'
import {Utils} from '../../utils'
import Button from '../../widgets/buttons/button'
import Menu from '../../widgets/menu'
import MenuWrapper from '../../widgets/menuWrapper'

import {contentRegistry} from '../content/contentRegistry'

import {useCardDetailContext} from './cardDetailContext'

function AddContentMenuItem(props: {intl: IntlShape, type: BlockTypes}): JSX.Element {
    const {intl, type} = props
    const handler = contentRegistry.getHandler(type)
    const cardDetail = useCardDetailContext()
    const addElement = useCallback(async () => {
        if (!handler) {
            return
        }
        const {card} = cardDetail
        const index = card.fields.contentOrder.length
        cardDetail.addBlock(handler, index, false)
    }, [cardDetail, handler])

    if (!handler) {
        Utils.logError(`AddContentMenuItem, unknown content type: ${type}`)
        return <></>
    }

    return (
        <Menu.Text
            id={type}
            name={handler.getDisplayText(intl)}
            icon={handler.getIcon()}
            onClick={addElement}
        />
    )
}

const CardDetailContentsMenu = () => {
    const intl = useIntl()
    return (
        <div className='CardDetailContentsMenu content add-content'>
            <MenuWrapper>
                <Button>
                    <FormattedMessage
                        id='CardDetail.add-content'
                        defaultMessage='Add content'
                    />
                </Button>
                <Menu position='top'>
                    {contentRegistry.contentTypes.map((type) => (
                        <AddContentMenuItem
                            key={type}
                            intl={intl}
                            type={type}
                        />
                    ))}
                </Menu>
            </MenuWrapper>
        </div>
    )
}

export default React.memo(CardDetailContentsMenu)
