---
name: Registry
description: A private registry console in the Docker Hub layout.
colors:
  blue: "#1D63ED"
  blue-hover: "#174EB8"
  blue-soft: "#E8F0FE"
  text: "#141414"
  muted: "#3D4451"
  line: "#E1E4E8"
  page: "#F4F5F7"
  white: "#FFFFFF"
  danger: "#B42318"
typography:
  display:
    fontFamily: "Roboto, sans-serif"
    fontSize: "32px"
    fontWeight: 500
    lineHeight: 1.2
    letterSpacing: "-0.02em"
  body:
    fontFamily: "Roboto, sans-serif"
    fontSize: "14px"
    fontWeight: 400
    lineHeight: 1.5
  label:
    fontFamily: "Roboto, sans-serif"
    fontSize: "12px"
    fontWeight: 500
    lineHeight: 1.5
  mono:
    fontFamily: "Roboto Mono, ui-monospace, monospace"
    fontSize: "12.5px"
    fontWeight: 400
    lineHeight: 1.55
rounded:
  control: "4px"
  panel: "8px"
spacing:
  page: "24px"
  row: "14px"
components:
  button-primary:
    backgroundColor: "{colors.blue}"
    textColor: "{colors.white}"
    rounded: "{rounded.control}"
    padding: "8px 12px"
    typography: "{typography.body}"
  button-primary-hover:
    backgroundColor: "{colors.blue-hover}"
    textColor: "{colors.white}"
    rounded: "{rounded.control}"
    padding: "8px 12px"
  button-delete:
    backgroundColor: "{colors.white}"
    textColor: "{colors.danger}"
    rounded: "{rounded.control}"
    padding: "8px 12px"
---

# Design System: Registry

## Overview

**Creative North Star: "Docker Hub"**

The dashboard is the Hub page for a registry that lives on one machine.
A white bar across the top, a gray page, and white panels with a hairline
border. Repository names are blue links. Image history is a scannable
list: one command per row, the size on the right, the full command when
the row is opened.

Roboto and Roboto Mono are self-hosted. The mark is a stack of blocks in
Docker blue. It is not the Docker whale.

**Key Characteristics:**

- Top bar, then a page title, then a table or a layer list.
- Blue for links and the primary button. Red only for delete.
- Digests are shortened in the table and copied in full.
- A layer command stays on one line until it is opened.

## Colors

The page is cool gray. Panels are white. Blue does the navigation work.
Delete red is only on a delete control.

### Primary

- **Docker blue** (`colors.blue`): Links, the current tab, the primary
  button, and the mark.

### Tertiary

- **Delete** (`colors.danger`): Delete buttons and the text of a failed
  sign-in.

### Neutral

- **Page** (`colors.page`): The background behind panels.
- **White** (`colors.white`): The top bar and every panel.
- **Text** (`colors.text`): Headings and body.
- **Muted** (`colors.muted`): Secondary labels. Dark enough to read on
  white.
- **Line** (`colors.line`): Panel, table, and input borders.

**The One Blue Rule.** Blue is for navigation and the main action. It is
not a background wash.

## Typography

**Display Font:** Roboto
**Body Font:** Roboto
**Mono Font:** Roboto Mono, for commands and digests.

### Hierarchy

- **Display** (500, 32px): The page title. A repository name or "Sign in".
- **Body** (400, 14px): Tables, forms, and sentences.
- **Label** (500, 12px): Table headers and stat labels.
- **Mono** (400, 12.5px): Commands and digests.

**The One Line Rule.** In the layer list, the command is one line with
an ellipsis. Opening the row shows the whole command, with runs of
spaces collapsed so a long `RUN` stays readable.

## Layout

A 64px top bar. The page content is centered at 1120px. Below 800px the
bar wraps and tables become stacked rows. Repository search sits above
the table, not in the bar.

## Elevation & Depth

Flat panels. A 1px line and an 8px corner separate a panel from the
page. Focus on a field is a blue border plus a soft blue ring. There is
no drop shadow under panels.

## Shapes

Controls are 4px. Panels and the sign-in form are 8px. The layer chevron
turns 90 degrees when a row opens, and that motion is skipped when the
reader asks for reduced motion.

## Components

### Buttons

- **Primary:** Blue fill, white text.
- **Ghost:** White fill, gray border. Used for Dry run and Cancel.
- **Delete:** White fill, delete-colored text, a pale red border.
  Confirmation stays on the page.

### Inputs / Fields

- **Style:** 1px border, 4px corner, white fill.
- **Focus:** Blue border and a soft blue ring.

### Navigation

- **Style:** Words in the top bar. The current item is blue on a pale
  blue chip.

### Image layers

A bordered list. Each row is a chevron, the command, and the size.
Opening it shows the full command in a mono block and a copy control
for the original text.

## Do's and Don'ts

### Do:

- **Do** keep the Hub reading order: title, meta, table or layers.
- **Do** let a layer row open to the full command.
- **Do** self-host Roboto and Roboto Mono.

### Don't:

- **Don't** paint a full-height color rail.
- **Don't** show a history command as one stretched preformatted line.
- **Don't** use the Docker whale. The block mark is enough.
