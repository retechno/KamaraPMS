import { clsx, type ClassValue } from 'clsx'
import { twMerge } from 'tailwind-merge'

/** Joins class names and lets the last Tailwind utility win (the helper shadcn-vue components use). */
export function cn(...inputs: ClassValue[]): string {
  return twMerge(clsx(inputs))
}
