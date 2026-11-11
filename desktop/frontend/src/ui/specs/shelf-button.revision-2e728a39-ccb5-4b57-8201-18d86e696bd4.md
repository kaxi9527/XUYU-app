# ShelfButton

Product: workshelf
Catalog id: shelf-button

## Overview

MDUI 2 button for WorkShelf confirmations and project file workflows, with escaped slot labels and shared neutral theme.

## Anatomy

- MDUI mdui-button host
- Native button inside the component shadow root
- Escaped visible slot label

## Variants

- neutral
- primary
- danger
- sm
- md

## Props

- label: string; required. Visible, escaped button label
- tone: neutral | primary | danger; optional. WorkShelf neutral, primary and danger variants on MDUI
- value: string; required. Existing data-x confirmation contract
- id: string; optional. Stable DOM event target for the existing page controller
- size: sm | md; optional. Library size, default sm; organizer uses md
- disabled: boolean; optional. Disables the underlying MDUI native control

## States

- default
- hover
- focus-visible
- disabled

## Behavior

- Preserves existing confirmation result callbacks
- Uses MDUI disabled property and native keyboard activation

## Accessibility

- MDUI native shadow button handles keyboard activation and disabled state
- Slot labels name the native control; icon actions include screen reader text

## Usage

- Shared desktop confirmation actions
- Primary and back actions in the organizer wizard
- AI source enrollment review, daily authorization and per-tool pause actions
- Project file memory enable, manual check, pause, receipt recovery and bounded history navigation
- Separate automatic project observation opt-in, pause and manual check actions
- Explicit project-analysis preview, consent, cancellation and local receipt recovery
- Project analysis directory browsing, bounded group preview and range changes
- Whole-directory batch consent, separate project quota change, pause/resume and aggregate result actions
- Read-only archived analysis receipts, revision-bound history pagination and refresh after failure
- Subscription recipient evidence on single and batch preview, result and resume; unchanged consent controls
- Read-only provider debit receipts inside original analysis records; no new controls or price guarantees
- Grouped project placement confirmation, original-request readback, execution stop and native undo actions
- Explicit background and login-startup settings with verified saved state and failure readback
- Project-scoped realtime observation status and visible periodic fallback; existing grant and revoke actions
- Project-specific source file consent, grant recovery, revoke and provenance evidence using the existing action component
- Session directory consent and common-location actions
- Automatic project creation, ongoing collection, project memory and objection restoration actions
- Worker objection command and provenance on automatic project history
- Confirm one work type for an unresolved project group
- Assign a project and trade to one unrecognized session group
- Gemini session directory authorization and automatic project collection

## Avoid

- Do not replace links with action buttons

## Implementation

- src/ui/ShelfButton.tsx

## Consumers

- src/ui-actions.tsx

Local UXQA checks do not replace the product component-library approval process.
