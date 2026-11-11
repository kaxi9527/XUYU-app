# ShelfButton

Product: workshelf
Catalog id: shelf-button

## Overview

Native daisyUI button for confirmation and the simple organizer flow, preserving escaping and native keyboard behavior.

## Anatomy

- Native button
- Text label

## Variants

- neutral
- primary
- danger
- sm
- md

## Props

- label: string; required. Visible, escaped button label
- tone: neutral | primary | danger; optional. daisyUI semantic variant
- value: string; required. Existing data-x confirmation contract
- id: string; optional. Stable DOM event target for the existing page controller
- size: sm | md; optional. Library size, default sm; organizer uses md

## States

- default
- hover
- focus-visible
- disabled

## Behavior

- Keeps native focus and keyboard activation
- Preserves existing confirmation result callbacks

## Accessibility

- Uses a native type=button element
- Visible label and disabled state remain available to assistive technology

## Usage

- Shared desktop confirmation actions
- Primary and back actions in the organizer wizard
- AI source enrollment review, daily authorization and per-tool pause actions
- Project file memory enable, manual check, pause, receipt recovery and bounded history navigation

## Avoid

- Do not replace links with action buttons

## Implementation

- src/ui/ShelfButton.tsx

## Consumers

- src/ui-actions.tsx

Local UXQA checks do not replace the product component-library approval process.
