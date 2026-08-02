// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

// Dragging a card has repeatedly worked for a handful of drags and then stopped
// dead -- no drag starts, the board looks untouched, and nothing is logged. None
// of it reproduces in jsdom: the failure lives in real layout, real pointer
// events and real timing, and jest has none of the three.
//
// So it is reproduced here, as a hand would do it. cypress-real-events sends
// CDP-level pointer events, which is also the only kind dnd-kit's sensor looks
// at -- it tests every event with `instanceof PointerEvent` and ignores the
// rest. The path is interpolated in small steps across the board rather than
// jumped in two, because how many pointermove events arrive, and how far apart,
// is exactly what activation constraints and collision detection react to.

const DRAGS = 30
const CARDS = 4
const STEPS = 14

function column(index: number) {
    return cy.get('.octo-board-column').eq(index)
}

function cardsIn(index: number) {
    return column(index).find('.KanbanCard')
}

function centreOf($element: JQuery): {x: number, y: number} {
    const rect = $element[0].getBoundingClientRect()
    return {x: Math.round(rect.left + (rect.width / 2)), y: Math.round(rect.top + (rect.height / 2))}
}

// Every move is expressed as an offset from the destination column's centre,
// because the column is the one thing that does not move during the drag: the
// card follows the pointer, and the body is not a reliable origin. The offset
// shrinks to zero, so the pointer walks from the card to the column.
function dragCard(from: number, to: number) {
    column(from).find('.KanbanCard').first().then(($card) => {
        const start = centreOf($card)

        column(to).then(($column) => {
            const end = centreOf($column)
            const dx = start.x - end.x
            const dy = start.y - end.y

            cy.wrap($card).realMouseDown({position: 'center'})

            for (let step = 1; step <= STEPS; step++) {
                const remaining = 1 - (step / STEPS)
                column(to).realMouseMove(Math.round(dx * remaining), Math.round(dy * remaining), {position: 'center'})
            }

            column(to).realMouseUp({position: 'center'})
        })
    })
}

// A card dialog is a portal mounted over the board and torn down again, which
// is the most ordinary thing to do between two drags and the one thing sixty
// consecutive drags never did.
function openAndCloseACard() {
    cy.get('.KanbanCard').first().realClick()
    cy.findByRole('dialog').should('exist')
    cy.findByRole('button', {name: 'Close dialog'}).click()
    cy.findByRole('dialog').should('not.exist')
}

// Press, twitch, release: a drag that never became one, because the pointer
// did not travel the five pixels the sensor asks for. People produce these
// constantly -- reaching for a card and changing their mind, or just a shaky
// click -- and the sensor has to come out of it as clean as it went in.
function nudgeACard() {
    cy.get('.KanbanCard').first().realMouseDown({position: 'center'})
    cy.get('.KanbanCard').first().realMouseMove(2, 2, {position: 'center'})
    cy.get('.KanbanCard').first().realMouseUp({position: 'center'})

    // A press and a twitch is still a click, so the card opens. Shut it again:
    // a dialog left over the board catches the next press, and that is the
    // test's doing rather than the board's.
    cy.get('body').then(($body) => {
        if ($body.find('.dialog-back').length > 0) {
            cy.findByRole('button', {name: 'Close dialog'}).click()
            cy.findByRole('dialog').should('not.exist')
        }
    })
}

describe('Kanban drag and drop', () => {
    beforeEach(() => {
        cy.apiInitServer()
        cy.apiResetBoards()
        cy.apiGetMe().then((userID) => cy.apiSkipTour(userID))
    })

    it('keeps working over a long run of consecutive drags', () => {
        cy.visit('/')
        cy.uiCreateNewBoard('Dragging')
        cy.uiAddNewGroup('Elsewhere')

        // Scoped to the column: the board has a "+ New" in its header too, and
        // findByRole refuses to choose between them.
        cy.log(`**Add ${CARDS} cards to the first group**`)
        for (let i = 0; i < CARDS; i++) {
            column(0).findByRole('button', {name: '+ New'}).click()
            cy.findByRole('dialog').should('exist')
            cy.findByRole('button', {name: 'Close dialog'}).click()
            cy.findByRole('dialog').should('not.exist')
        }
        cardsIn(0).should('have.length', CARDS)

        // Every iteration ends with the board exactly as it started, so a late
        // failure means the mechanism wore out rather than the board drifting
        // into a state nobody would reach by hand.
        //
        // Interleaved with what a person actually does between drags: opening a
        // card and closing it again. Sixty uninterrupted drags pass; the board
        // still dies in use, so the difference is in what happens in between.
        for (let i = 0; i < DRAGS; i++) {
            cy.log(`**Drag ${i + 1} of ${DRAGS}: across and back**`)

            dragCard(0, 1)
            cardsIn(1).should('have.length', 1)
            cardsIn(0).should('have.length', CARDS - 1)

            if (i % 2 === 0) {
                openAndCloseACard()
            } else {
                nudgeACard()
            }

            dragCard(1, 0)
            cardsIn(0).should('have.length', CARDS)
            cardsIn(1).should('have.length', 0)
        }
    })
})
