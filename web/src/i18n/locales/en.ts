/**
 * English messages: the source of truth for the keys. The other languages are typed against this object, so a key that
 * is missing in one of them fails the type check. Group keys by where they are shown, not by page.
 */
export const en = {
  language: {
    label: 'Language',
    en: 'English',
    id: 'Bahasa Indonesia',
  },
  common: {
    save: 'Save',
    cancel: 'Cancel',
    close: 'Close',
    delete: 'Delete',
    edit: 'Edit',
    create: 'Create',
    search: 'Search',
    refresh: 'Refresh',
    loading: 'Loading…',
    back: 'Back',
    next: 'Next',
    confirm: 'Confirm',
    yes: 'Yes',
    no: 'No',
    noResults: 'Nothing to show.',
    required: 'Required',
  },
}

export type Messages = typeof en
