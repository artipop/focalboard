// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import React, {type CSSProperties, type JSX, useEffect, useId, useRef, useState} from 'react'
import ReactDOM from 'react-dom'
import {FormattedMessage} from 'react-intl'
import {arrow, autoUpdate, computePosition, flip, offset, shift, type Placement} from '@floating-ui/dom'

import './tutorial_tour_tip.scss'

import CloseIcon from '../../widgets/icons/close'
import Button from '../../widgets/buttons/button'
import IconButton from '../../widgets/buttons/iconButton'
import CompassIcon from '../../widgets/icons/compassIcon'

import PulsatingDot from '../pulsating_dot'

import TutorialTourTipBackdrop, {Coords, TutorialTourTipPunchout} from './tutorial_tour_tip_backdrop'

import useTutorialTourTipManager, {TutorialTourTipManagerProps} from './tutorial_tour_tip_manager'

const TourTipOverlay = ({
    children,
    show,
    onClick,
}: { children: React.ReactNode, show: boolean, onClick: (e: React.MouseEvent) => void }) =>
    (show ? ReactDOM.createPortal(
        <div
            className='tutorial-tour-tip__overlay'
            onClick={onClick}
        >
            {children}
        </div>,
        document.body,
    ) : null)

// The arrow is a 12px square turned 45 degrees, so its corner reaches about
// 8.5px out of the box; 10px of offset keeps it clear of what it points at,
// which is the gap tippy left as well.
const ARROW_SIZE = 12
const TIP_OFFSET = 10
const VIEWPORT_PADDING = 8

const oppositeSide: Record<string, 'top' | 'right' | 'bottom' | 'left'> = {
    top: 'bottom',
    right: 'left',
    bottom: 'top',
    left: 'right',
}

type TipPosition = {
    x: number
    y: number
    placement: Placement
    arrowX?: number
    arrowY?: number
}

type TourTipBoxProps = {
    anchor: React.RefObject<HTMLElement | null>
    placement?: Placement
    width: string | number
    className?: string
    labelledBy: string
    children: React.ReactNode
}

// What Tippy was here for. The box is portalled to the body and placed against
// the pulsating dot by Floating UI, which — unlike tippy.js, whose author moved
// on to write it — has no framework in it: a port would keep everything below
// the return statement and rewrite the rest.
//
// The translation goes on the positioner and the entrance animation on the box
// inside it, so the two never fight over `transform`.
const TourTipBox = (props: TourTipBoxProps): JSX.Element => {
    const [positioner, setPositioner] = useState<HTMLDivElement | null>(null)
    const [arrowElement, setArrowElement] = useState<HTMLDivElement | null>(null)
    const [position, setPosition] = useState<TipPosition | null>(null)

    const {anchor, placement} = props

    useEffect(() => {
        const reference = anchor.current
        if (!reference || !positioner || !arrowElement) {
            return undefined
        }

        // Keeps the box on the dot while the board behind it scrolls or resizes.
        return autoUpdate(reference, positioner, () => {
            computePosition(reference, positioner, {
                placement: placement || 'top',
                middleware: [
                    offset(TIP_OFFSET),
                    flip(),
                    shift({padding: VIEWPORT_PADDING}),
                    arrow({element: arrowElement, padding: VIEWPORT_PADDING}),
                ],
            }).then((computed) => {
                setPosition({
                    x: computed.x,
                    y: computed.y,
                    placement: computed.placement,
                    arrowX: computed.middlewareData.arrow?.x,
                    arrowY: computed.middlewareData.arrow?.y,
                })
            })
        })
    }, [anchor, positioner, arrowElement, placement])

    const resolved = position?.placement || placement || 'top'
    const arrowStyle: CSSProperties = {
        left: position?.arrowX === undefined ? undefined : `${position.arrowX}px`,
        top: position?.arrowY === undefined ? undefined : `${position.arrowY}px`,
        [oppositeSide[resolved.split('-')[0]]]: `${-ARROW_SIZE / 2}px`,
    }

    return ReactDOM.createPortal(
        <div
            ref={setPositioner}

            // Nothing is drawn until there is somewhere to draw it, so the box
            // never appears at the top-left corner for a frame first.
            className={`tutorial-tour-tip__positioner ${position ? 'is-positioned' : ''}`}
            data-placement={resolved}
            style={position ? {transform: `translate(${Math.round(position.x)}px, ${Math.round(position.y)}px)`} : undefined}
        >
            <div
                className={`tutorial-tour-tip__box ${props.className || ''}`}
                style={{maxWidth: props.width}}
                role='dialog'
                aria-labelledby={props.labelledBy}
            >
                <div
                    ref={setArrowElement}
                    className='tutorial-tour-tip__arrow'
                    style={arrowStyle}
                />
                {props.children}
            </div>
        </div>,
        document.body,
    )
}

type Props = {
    screen: JSX.Element
    title: JSX.Element
    imageURL?: string
    punchOut?: TutorialTourTipPunchout | null
    step: number
    singleTip?: boolean
    showOptOut?: boolean
    placement?: Placement
    telemetryTag?: string
    stopPropagation?: boolean
    preventDefault?: boolean
    tutorialCategory: string
    onNextNavigateTo?: () => void
    onPrevNavigateTo?: () => void
    autoTour?: boolean
    pulsatingDotPosition?: Coords | undefined
    width?: string | number
    className?: string
    hideNavButtons?: boolean
    hideBackdrop?: boolean
    clickThroughPunchhole?: boolean
    onPunchholeClick?: (e: React.MouseEvent) => void
    skipCategoryFromBackdrop?: boolean
}

const TutorialTourTip = ({
    title,
    screen,
    imageURL,
    punchOut,
    autoTour,
    tutorialCategory,
    singleTip,
    step,
    onNextNavigateTo,
    onPrevNavigateTo,
    telemetryTag,
    placement,
    showOptOut,
    pulsatingDotPosition,
    stopPropagation = true,
    preventDefault = true,
    width = window.innerWidth > 2559 ? 500 : 320,
    className,
    hideNavButtons = false,
    hideBackdrop = false,
    onPunchholeClick,
    skipCategoryFromBackdrop,
}: Props): JSX.Element => {
    const managerProps: TutorialTourTipManagerProps = {
        step,
        autoTour,
        telemetryTag,
        tutorialCategory,
        onNextNavigateTo,
        onPrevNavigateTo,
        stopPropagation,
        preventDefault,
    }

    const triggerRef = useRef<HTMLDivElement>(null)
    const titleId = useId()
    const {
        show,
        tourSteps,
        handleOpen,
        handleHide,
        handleDismiss,
        handleNext,
        handlePrevious,
        handleSkipTutorial,
        handleSavePreferences,
        getLastStep,
        handleEventPropagationAndDefault,
        handleSendToNextTour,
    } = useTutorialTourTipManager(managerProps)

    const getButtonText = (): JSX.Element => {
        let buttonText = (
            <FormattedMessage
                id={'tutorial_tip.ok'}
                defaultMessage={'Next'}
            />
        )
        if (singleTip) {
            buttonText = (
                <FormattedMessage
                    id={'tutorial_tip.got_it'}
                    defaultMessage={'Got it'}
                />
            )
            return buttonText
        }

        const lastStep = getLastStep()
        if (step === lastStep) {
            buttonText = (
                <FormattedMessage
                    id={'tutorial_tip.finish_tour'}
                    defaultMessage={'Done'}
                />
            )
        }

        return buttonText
    }

    const dots = []

    if (!singleTip && tourSteps) {
        for (let i = 0; i < (Object.values(tourSteps).length); i++) {
            let classname = 'tutorial-tour-tip__circle'
            let circularRing = 'tutorial-tour-tip__circular-ring'

            if (i === step) {
                classname += ' active'
                circularRing += ' tutorial-tour-tip__circular-ring-active'
            }

            dots.push(
                <div
                    key={'dotactive' + i}
                    className={circularRing}
                >
                    <a
                        href='#'
                        className={classname}
                        data-screen={i}
                        onClick={() => handleSavePreferences(i)}
                    />
                </div>,
            )
        }
    }

    const content = (
        <div
            onClick={(e) => {
                e.stopPropagation()
            }}
        >
            <div className='tutorial-tour-tip__header'>
                <h4
                    id={titleId}
                    className='tutorial-tour-tip__header__title'
                >
                    {title}
                </h4>
                <IconButton
                    className='tutorial-tour-tip__header__close'
                    size='small'
                    icon={<CloseIcon/>}
                    onClick={(e) => {
                        if (skipCategoryFromBackdrop) {
                            handleDismiss(e)
                            handleSendToNextTour(tutorialCategory)
                        }
                    }}
                />
            </div>
            <div className='tutorial-tour-tip__body'>
                {screen}
            </div>
            {imageURL && (
                <div className='tutorial-tour-tip__image'>
                    <img
                        src={imageURL}
                        alt={'tutorial tour tip product image'}
                    />
                </div>
            )}
            <div className='tutorial-tour-tip__footer'>
                <div className='tutorial-tour-tip__footer-buttons'>
                    <div className='tutorial-tour-tip__circles-ctr'>{dots}</div>
                    <div className={'tutorial-tour-tip__btn-ctr'}>
                        {!hideNavButtons && step !== 0 && (
                            <Button
                                title='Previous'
                                size='small'
                                emphasis='tertiary'
                                onClick={handlePrevious}
                                icon={
                                    <CompassIcon
                                        className='icon'
                                        icon='chevron-left'
                                    />}
                            >
                                <FormattedMessage
                                    id='generic.previous'
                                    defaultMessage='Previous'
                                />
                            </Button>
                        )}

                        {
                            !hideNavButtons && (
                                <Button
                                    className='tipNextButton'
                                    size='small'
                                    filled={true}
                                    onClick={handleNext}
                                    rightIcon={true}
                                    icon={(singleTip || step === getLastStep()) ? '' : (
                                        <CompassIcon
                                            className='icon'
                                            icon='chevron-right'
                                        />
                                    )
                                    }
                                >
                                    {getButtonText()}
                                </Button>
                            )
                        }
                    </div>
                </div>
                {showOptOut && <div className='tutorial-tour-tip__opt'>
                    <FormattedMessage
                        id='tutorial_tip.seen'
                        defaultMessage='Seen this before? '
                    />
                    <a
                        href='#'
                        onClick={handleSkipTutorial}
                    >
                        <FormattedMessage
                            id='tutorial_tip.out'
                            defaultMessage='Opt out of these tips.'
                        />
                    </a>
                </div>}
            </div>
        </div>
    )

    return (
        <>
            <div
                ref={triggerRef}
                onClick={handleOpen}
                aria-expanded={show}
                className={`tutorial-tour-tip__pulsating-dot-ctr ${className || ''}`}
            >
                <PulsatingDot coords={pulsatingDotPosition}/>
            </div>
            <TourTipOverlay
                show={!hideBackdrop && show}
                onClick={(e) => {
                    handleEventPropagationAndDefault(e)
                    handleHide(e)
                    if (onPunchholeClick) {
                        onPunchholeClick(e)
                    }
                }}
            >
                <TutorialTourTipBackdrop
                    x={punchOut?.x}
                    y={punchOut?.y}
                    width={punchOut?.width}
                    height={punchOut?.height}
                    handleClick={(e) => {
                        if (skipCategoryFromBackdrop) {
                            e.preventDefault()
                            e.stopPropagation()
                            handleSendToNextTour(tutorialCategory)
                        }
                    }}
                />
            </TourTipOverlay>
            {show && (
                <TourTipBox
                    anchor={triggerRef}
                    placement={placement}
                    width={width}
                    className={className}
                    labelledBy={titleId}
                >
                    {content}
                </TourTipBox>
            )}
        </>
    )
}

export default TutorialTourTip
