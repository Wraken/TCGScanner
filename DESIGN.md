---
name: TCGScanner
description: Nocturne, the dark desktop UI of a local trading-card scanner
colors:
  night-ink: "#161826"
  desk-slate: "#1d1f2e"
  raised-slate: "#232532"
  hairline-indigo: "#262940"
  lamp-lavender: "#9184d9"
  soft-lavender: "#c8c2ec"
  deep-lavender: "#423a6a"
  pale-lavender: "#f5f4ff"
  moon-white: "#e9e9ed"
  bright-mist: "#f3f5fe"
  mist-400: "#b2b6ca"
  mist-500: "#9397ab"
  mist-600: "#75798c"
  mist-700: "#595d6c"
  mist-800: "#3f424d"
  coral-alert: "#f07178"
typography:
  display:
    fontFamily: "Inter, -apple-system, BlinkMacSystemFont, Segoe UI, sans-serif"
    fontSize: "40px"
    fontWeight: 500
    lineHeight: 1
  headline:
    fontFamily: "Inter, -apple-system, BlinkMacSystemFont, Segoe UI, sans-serif"
    fontSize: "32px"
    fontWeight: 500
    lineHeight: 1.25
    letterSpacing: "-0.015em"
  title:
    fontFamily: "Inter, -apple-system, BlinkMacSystemFont, Segoe UI, sans-serif"
    fontSize: "18px"
    fontWeight: 500
    lineHeight: 1.25
  body:
    fontFamily: "Inter, -apple-system, BlinkMacSystemFont, Segoe UI, sans-serif"
    fontSize: "14px"
    fontWeight: 400
    lineHeight: 1.5
  label:
    fontFamily: "Inter, -apple-system, BlinkMacSystemFont, Segoe UI, sans-serif"
    fontSize: "11px"
    fontWeight: 400
    letterSpacing: "0.08em"
  data:
    fontFamily: "ui-monospace, SFMono-Regular, Menlo, Consolas, monospace"
    fontSize: "11px"
    fontWeight: 400
rounded:
  sm: "6px"
  md: "8px"
spacing:
  xs: "6px"
  sm: "8px"
  md: "16px"
  lg: "28px"
  xl: "44px"
components:
  button-primary:
    backgroundColor: "transparent"
    textColor: "{colors.lamp-lavender}"
    rounded: "{rounded.md}"
    padding: "8px 14px"
  button-secondary:
    backgroundColor: "transparent"
    textColor: "{colors.moon-white}"
    rounded: "{rounded.md}"
    padding: "8px 14px"
  button-danger:
    backgroundColor: "transparent"
    textColor: "{colors.coral-alert}"
    rounded: "{rounded.md}"
    padding: "8px 14px"
  chip:
    backgroundColor: "transparent"
    textColor: "{colors.mist-400}"
    rounded: "{rounded.sm}"
    padding: "4px 10px"
  chip-selected:
    backgroundColor: "{colors.deep-lavender}"
    textColor: "{colors.lamp-lavender}"
    rounded: "{rounded.sm}"
    padding: "4px 10px"
  tag-accent:
    backgroundColor: "{colors.deep-lavender}"
    textColor: "{colors.pale-lavender}"
    rounded: "{rounded.sm}"
    padding: "3px 10px"
  tag-neutral:
    backgroundColor: "{colors.mist-800}"
    textColor: "{colors.bright-mist}"
    rounded: "{rounded.sm}"
    padding: "3px 10px"
  input:
    backgroundColor: "{colors.desk-slate}"
    textColor: "{colors.moon-white}"
    rounded: "{rounded.sm}"
    padding: "8px 12px"
  nav-item:
    backgroundColor: "transparent"
    textColor: "{colors.mist-400}"
    rounded: "{rounded.md}"
    padding: "8px 12px"
  tile:
    backgroundColor: "{colors.desk-slate}"
    textColor: "{colors.moon-white}"
    rounded: "{rounded.md}"
---

# Design System: TCGScanner

## Overview

**Creative North Star: "The Night Desk"**

A collector's desk in the evening: dark, quiet surfaces and a single lavender lamp that pulls the eye to the card in hand. The interface is an instrument for a focused, repetitive task (hold a card up, confirm, save, next), so it stays calm and precise and keeps out of the way of the camera feed and the card artwork, which are the only things allowed to be loud.

Density is that of a daily desktop tool: a fixed left rail, generous padding around each screen, compact controls. Depth comes from tonal layering of near-black indigo surfaces and 1px hairlines, not from shadows. The accent is rare on purpose: it marks the primary action, the current selection, focus, and the detection box. Everything else is a cool neutral.

The app is dark-only. There is no light theme and the browser surfaces (scrollbars, checkboxes, sliders, native dialogs, text selection, caret) are themed to match.

**Key Characteristics:**
- One accent (lamp lavender) on a cool indigo-neutral ramp.
- Flat surfaces, hairline borders, outline buttons; shadows only for hover lift and overlays.
- Inter 400/500 only; monospace reserved for card IDs and data.
- Motion is feedback and state only, with a reduced-motion path for all of it.

## Colors

A restrained palette: deep indigo neutrals, one lavender accent with a soft and a deep step, and one coral for errors.

### Primary
- **Lamp Lavender** (#9184d9): primary button text and outline, current nav item, selected chip and segment, focus ring, detection box stroke, foil marker, inline links. 5.5:1 on Night Ink.
- **Soft Lavender** (#c8c2ec): accent text where Lamp Lavender would be too dim (hover on nav, detection label, outline tags).
- **Deep Lavender** (#423a6a): background of accent tags ("Card detected", card colours) and of selected chips (as a 10% tint of the lamp).
- **Pale Lavender** (#f5f4ff): text on Deep Lavender.

### Neutral
- **Night Ink** (#161826): the page and dialog background; also the Wails window background.
- **Desk Slate** (#1d1f2e): raised surfaces: tiles, inputs, the camera viewport, the crop box, select popovers, notices.
- **Raised Slate** (#232532): secondary fills.
- **Hairline Indigo** (#262940): every divider and resting border.
- **Moon White** (#e9e9ed): primary text.
- **Mist 400** (#b2b6ca): secondary text and form labels.
- **Mist 500** (#9397ab): tertiary text, icons, placeholders (6:1 on Night Ink).
- **Mist 600** (#75798c): disabled text and empty-image icons only, never readable copy (4.1:1).
- **Mist 700 / 800** (#595d6c / #3f424d): hover borders, neutral tag fill, scrollbar thumb.
- **Bright Mist** (#f3f5fe): text on neutral tags.

### Semantic
- **Coral Alert** (#f07178): error text, the danger button and the error notice outline. 6.1:1 on Night Ink, 5.7:1 on Desk Slate.

### Named Rules
**The One Lamp Rule.** Lamp Lavender only marks the primary action, the current selection, focus and live detection. It never decorates.

**The Readable Error Rule.** Error text uses Coral Alert, never a dark red: an error the user cannot read is a silent failure.

## Typography

**Body Font:** Inter (self-hosted woff2, weights 400 and 500), with the system sans as fallback.
**Data Font:** the system monospace stack, for card IDs, CSV previews and the detection label.

**Character:** one neutral sans carries every role; hierarchy comes from size and the 400/500 step, not from extra families or heavier weights.

### Hierarchy
- **Display** (500, 40px, line-height 1): the stat values on Home, tabular figures.
- **Headline** (500, 32px on Home and 28px on Collections, line-height 1.25, -0.015em): the one screen title.
- **Title** (500, 18px, 1.25): dialog titles.
- **Body** (400, 14px, 1.5): buttons, inputs, list text; 12–13px for secondary lines and notices.
- **Label** (400, 11px, uppercase, 0.08em tracking): stat labels and the small section headings ("Recent scans", "Custom collections").
- **Data** (monospace, 11–14px): card IDs everywhere, the Card ID field, CSV preview.

### Named Rules
**The Two Weights Rule.** Only 400 and 500 are loaded. Never ask for 600 or bold: the browser would fake it.

## Layout

A fixed 216px left rail (brand, Home, Scan, Collections, Export, Settings), then one scrollable screen. Screens pad 28–44px. The Scan screen splits into the camera column (flexible) and a capture panel (320–380px, fluid with the window). Grids use `auto-fill` tracks (game tiles from 260px, card thumbnails from 150px) so nothing wraps awkwardly at the 1024×640 minimum window. Card images keep the card's 63:88 ratio.

## Elevation & Depth

Flat by default. Hierarchy comes from the surface ladder (Night Ink, Desk Slate, Raised Slate) and Hairline Indigo borders.

### Shadow Vocabulary
- **Hover lift** (`box-shadow: 0 0 0 1px #595d6c, 0 6px 18px rgba(0,0,0,0.55)`): game tiles on hover; notices at rest.
- **Overlay** (`box-shadow: 0 16px 40px rgba(0,0,0,0.6)`): dialogs; select popovers use `0 6px 18px rgba(0,0,0,0.55)`.
- **Detection glow** (`drop-shadow(0 0 5px` lavender at 55%`)`): the detection box only, pulsing gently unless reduced motion is on.

### Named Rules
**The Flat-At-Rest Rule.** Surfaces are flat at rest. A shadow appears only as a response to hover, or because something floats above the page (dialog, popover, notice).

## Shapes

Gently rounded: 8px for buttons, tiles, panels and dialogs; 6px for inputs, chips, tags and small icon buttons; 4px for the recent-scan thumbnails. Camera aim brackets are 26px L-corners with an 8px outer radius. Borders are always 1px.

## Components

### Buttons
- **Shape:** gently rounded (8px), 1px border, never filled.
- **Primary:** Lamp Lavender text and outline; hover adds a 12% lavender tint, active 22%.
- **Secondary:** Moon White text, Hairline Indigo outline; hover 7% white tint.
- **Ghost:** Mist 500 text, no border; used for dismiss and close.
- **Danger:** Coral Alert text and a 55% coral outline; only in confirmations ("Delete collection", "Delete copy").
- **Focus:** the global 2px Lamp Lavender outline with a 2px offset.
- **Disabled:** 45% opacity.

### Chips
- **Style:** 12px text, 6px radius, 1px Hairline Indigo border, Mist 400 text.
- **State:** a pressed toggle (`aria-pressed`); selected = Lamp Lavender text and border on a 10% lavender tint. Used for collection views and export formats.

### Tags
- Small 11px labels with no border: accent (Deep Lavender) for detection state and card colours, neutral (Mist 800) for metadata, outline for "Suggested".

### Segmented control
- A rounded, clipped group of equal options (Normal / Foil, All / Normal / Foil). Selected = inset 1px lavender ring and lavender text. Focus is drawn inside the segment so the clipping never hides it.

### Inputs / Fields
- **Style:** Desk Slate fill, 1px Hairline Indigo border, 6px radius, Mist 500 placeholder.
- **Focus:** the global lavender outline; a field with a leading icon shows it on the wrapper.

### Navigation
- Rail items: 14px, Mist 400, 8px radius. Hover: Soft Lavender with a 6% lavender tint. Current screen (`aria-current="page"`): Lamp Lavender with a 10% tint. Export opens the export dialog rather than a screen. Settings is disabled with a "Soon" tag.

### Dialogs
- Night Ink panel, 8px radius, hairline border, overlay shadow, a ghost close button. Opens with a 200ms fade and slight scale; closes in 120ms. Reduced motion keeps only the fade.

### Notices
- Bottom-right stack of Desk Slate cards with an icon and a dismiss button. Successes fade after 5 seconds; errors (coral outline) stay until dismissed. They are live regions.

### Detection box (signature)
- A 2px Lamp Lavender rectangle over a 5px Night Ink halo so it reads on any background, with a monospace "card · 98.6%" label in Soft Lavender over a dark stroke. The camera frame is shown whole (contain), the same frame the detector sees.

## Do's and Don'ts

### Do:
- **Do** keep one accent: Lamp Lavender (#9184d9) for action, selection, focus and detection only.
- **Do** use Coral Alert (#f07178) for every error, next to what failed, and keep the user's input.
- **Do** build depth from the Night Ink, Desk Slate, Raised Slate ladder and 1px Hairline Indigo borders.
- **Do** give every control a visible focus state and every toggle an `aria-pressed` or `aria-current`.
- **Do** honour `prefers-reduced-motion`: keep fades that carry state, drop scale, slide and the detection pulse.

### Don't:
- **Don't** fill buttons with the accent; primary buttons are outlined.
- **Don't** use font weights other than 400 and 500.
- **Don't** use `window.prompt` or `window.confirm`; use the app's dialogs.
- **Don't** delete anything without a confirmation that names the action.
- **Don't** show a trademarked game logo; game tiles use the in-house artwork.
