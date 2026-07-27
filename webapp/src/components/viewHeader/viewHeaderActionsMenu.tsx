// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.
import React, {useState} from 'react'
import {useIntl, IntlShape} from 'react-intl'

import {CsvExporter} from '../../csvExporter'
import {Archiver} from '../../archiver'
import {Board} from '../../blocks/board'
import {BoardView} from '../../blocks/boardView'
import {Card} from '../../blocks/card'
import IconButton from '../../widgets/buttons/iconButton'
import OptionsIcon from '../../widgets/icons/options'
import Menu from '../../widgets/menu'
import MenuWrapper from '../../widgets/menuWrapper'
import {Utils} from '../../utils'

import ModalWrapper from '../modalWrapper'
import {sendFlashMessage} from '../flashMessages'
import AgentReposDialog, {isAgentReposAvailable} from '../acp/agentReposDialog'
import AgentsDialog, {isAgentsAvailable} from '../acp/agentsDialog'
import DeployTargetsDialog, {isDeployTargetsAvailable} from '../acp/deployTargetsDialog'
import PlanningDialog, {isPlanningAvailable} from '../acp/planningDialog'

type Props = {
    board: Board
    activeView: BoardView
    cards: Card[]
}

// import {mutator} from '../../mutator'
// import {CardFilter} from '../../cardFilter'
// import {BlockIcons} from '../../blockIcons'
// async function testAddCards(board: Board, activeView: BoardView, startCount: number, count: number) {
//     let optionIndex = 0

//     mutator.performAsUndoGroup(async () => {
//         for (let i = 0; i < count; i++) {
//             const card = new Card()
//             card.parentId = board.id
//             card.boardId = board.boardId
//             card.fields.properties = CardFilter.propertiesThatMeetFilterGroup(activeView.fields.filter, board.cardProperties)
//             card.title = `Test Card ${startCount + i + 1}`
//             card.fields.icon = BlockIcons.shared.randomIcon()

//             const groupByProperty = board.cardProperties.find((o) => o.id === activeView.fields.groupById)
//             if (groupByProperty && groupByProperty.options.length > 0) {
//                 // Cycle through options
//                 const option = groupByProperty.options[optionIndex]
//                 optionIndex = (optionIndex + 1) % groupByProperty.options.length
//                 card.fields.properties[groupByProperty.id] = option.id
//             }
//             mutator.insertBlock(card, 'test add card')
//         }
//     })
// }

// async function testDistributeCards(boardTree: BoardTree) {
//     mutator.performAsUndoGroup(async () => {
//         let optionIndex = 0
//         for (const card of boardTree.cards) {
//             if (boardTree.groupByProperty && boardTree.groupByProperty.options.length > 0) {
//                 // Cycle through options
//                 const option = boardTree.groupByProperty.options[optionIndex]
//                 optionIndex = (optionIndex + 1) % boardTree.groupByProperty.options.length
//                 const newCard = new Card(card)
//                 if (newCard.properties[boardTree.groupByProperty.id] !== option.id) {
//                     newCard.properties[boardTree.groupByProperty.id] = option.id
//                     mutator.updateBlock(newCard, card, 'test distribute cards')
//                 }
//             }
//         }
//     })
// }

// async function testRandomizeIcons(boardTree: BoardTree) {
//     mutator.performAsUndoGroup(async () => {
//         for (const card of boardTree.cards) {
//             mutator.changeIcon(card.id, card.fields.icon, BlockIcons.shared.randomIcon(), 'randomize icon')
//         }
//     })
// }

function onExportCsvTrigger(board: Board, activeView: BoardView, cards: Card[], intl: IntlShape) {
    try {
        CsvExporter.exportTableCsv(board, activeView, cards, intl)
        const exportCompleteMessage = intl.formatMessage({
            id: 'ViewHeader.export-complete',
            defaultMessage: 'Export complete!',
        })
        sendFlashMessage({content: exportCompleteMessage, severity: 'normal'})
    } catch (e) {
        Utils.logError(`ExportCSV ERROR: ${e}`)
        const exportFailedMessage = intl.formatMessage({
            id: 'ViewHeader.export-failed',
            defaultMessage: 'Export failed!',
        })
        sendFlashMessage({content: exportFailedMessage, severity: 'high'})
    }
}

const ViewHeaderActionsMenu = (props: Props) => {
    const {board, activeView, cards} = props
    const intl = useIntl()
    const [showAgentRepos, setShowAgentRepos] = useState(false)
    const [showAgents, setShowAgents] = useState(false)
    const [showDeployTargets, setShowDeployTargets] = useState(false)
    const [showPlanning, setShowPlanning] = useState(false)

    return (
        <ModalWrapper>
            <MenuWrapper label={intl.formatMessage({id: 'ViewHeader.view-header-menu', defaultMessage: 'View header menu'})}>
                <IconButton icon={<OptionsIcon/>}/>
                <Menu position='left'>
                    <Menu.Text
                        id='exportCsv'
                        name={intl.formatMessage({id: 'ViewHeader.export-csv', defaultMessage: 'Export to CSV'})}
                        onClick={() => onExportCsvTrigger(board, activeView, cards, intl)}
                    />
                    <Menu.Text
                        id='exportBoardArchive'
                        name={intl.formatMessage({id: 'ViewHeader.export-board-archive', defaultMessage: 'Export board archive'})}
                        onClick={() => Archiver.exportBoardArchive(board)}
                    />
                    {/* An empty array (unlike false/null) leaves no wrapper
                        div behind: Menu wraps every child slot in a div. */}
                    {isPlanningAvailable() ? [
                        <Menu.Text
                            key='planTask'
                            id='planTask'
                            name={intl.formatMessage({id: 'ViewHeader.plan-task', defaultMessage: 'Plan a task…'})}
                            onClick={() => setShowPlanning(true)}
                        />,
                    ] : []}
                    {isAgentReposAvailable() ? [
                        <Menu.Text
                            key='agentRepos'
                            id='agentRepos'
                            name={intl.formatMessage({id: 'ViewHeader.agent-repos', defaultMessage: 'Repositories…'})}
                            onClick={() => setShowAgentRepos(true)}
                        />,
                    ] : []}
                    {isAgentsAvailable() ? [
                        <Menu.Text
                            key='agents'
                            id='agents'
                            name={intl.formatMessage({id: 'ViewHeader.agents', defaultMessage: 'Agents…'})}
                            onClick={() => setShowAgents(true)}
                        />,
                    ] : []}
                    {isDeployTargetsAvailable() ? [
                        <Menu.Text
                            key='deployTargets'
                            id='deployTargets'
                            name={intl.formatMessage({id: 'ViewHeader.deploy-targets', defaultMessage: 'Deploy targets…'})}
                            onClick={() => setShowDeployTargets(true)}
                        />,
                    ] : []}
                    {/*
                    <Menu.Separator/>

                    <Menu.Text
                        id='testAdd100Cards'
                        name={intl.formatMessage({id: 'ViewHeader.test-add-100-cards', defaultMessage: 'TEST: Add 100 cards'})}
                        onClick={() => testAddCards(board, activeView, cards.length, 100)}
                    />
                    <Menu.Text
                        id='testAdd1000Cards'
                        name={intl.formatMessage({id: 'ViewHeader.test-add-1000-cards', defaultMessage: 'TEST: Add 1,000 cards'})}
                        onClick={() => testAddCards(board, activeView, cards.length, 1000)}
                    />
                    <Menu.Text
                        id='testDistributeCards'
                        name={intl.formatMessage({id: 'ViewHeader.test-distribute-cards', defaultMessage: 'TEST: Distribute cards'})}
                        onClick={() => testDistributeCards()}
                    />
                    <Menu.Text
                        id='testRandomizeIcons'
                        name={intl.formatMessage({id: 'ViewHeader.test-randomize-icons', defaultMessage: 'TEST: Randomize icons'})}
                        onClick={() => testRandomizeIcons()}
                    />
                    */}
                </Menu>
            </MenuWrapper>
            {showAgentRepos &&
                <AgentReposDialog
                    board={board}
                    onClose={() => setShowAgentRepos(false)}
                />}
            {showAgents &&
                <AgentsDialog
                    board={board}
                    onClose={() => setShowAgents(false)}
                />}
            {showDeployTargets &&
                <DeployTargetsDialog
                    onClose={() => setShowDeployTargets(false)}
                />}
            {showPlanning &&
                <PlanningDialog
                    board={board}
                    activeView={activeView}
                    onClose={() => setShowPlanning(false)}
                />}
        </ModalWrapper>
    )
}

export default React.memo(ViewHeaderActionsMenu)
