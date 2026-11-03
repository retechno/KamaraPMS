import type { Component } from 'vue'

/**
 * An action of a row of a table, told once and shown where it fits: in a row of a table (the main ones as buttons, the others in the "..." menu) and in a card on a phone
 * (one main action as a button, the others in the menu). An action is a link (`to`) or something that is done (`onSelect`).
 */
export interface RowAction {
  key: string
  label: string
  /** A page the action goes to. */
  to?: string
  /** What the action does, when it is not a link. */
  onSelect?: () => void
  /** A main action: shown as a button, not only in the menu. */
  primary?: boolean
  destructive?: boolean
  disabled?: boolean
  /** The look of the button of a main action: an outline unless said otherwise (the solid one is for the action that is the natural next step). */
  variant?: 'default' | 'outline'
  /** A mark before the label of the button (the menu shows the label alone). */
  icon?: Component
  /** The classes of that mark. */
  iconClass?: string
  /** The `data-testid` of the button or the menu item; `<key>` by default in the menu. */
  testId?: string
}
