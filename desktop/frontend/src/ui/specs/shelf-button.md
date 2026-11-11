# ShelfButton

Product: workshelf
Catalog id: shelf-button

## Overview

Native HTML button rendered with the pinned daisyUI component, preserving the desktop string-rendering architecture.

## Anatomy

- Native button
- Text label

## Variants

- neutral
- primary
- danger

## Props

- label: string; required. Visible, escaped button label
- tone: neutral | primary | danger; optional. daisyUI semantic variant
- value: string; required. Existing data-x confirmation contract

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

## Avoid

- Do not replace links with action buttons

## Implementation

- src/ui/ShelfButton.tsx

## Consumers

- src/ui-actions.tsx

Local UXQA checks do not replace the product component-library approval process.
