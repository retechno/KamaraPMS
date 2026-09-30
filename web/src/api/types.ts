import type { components } from './schema'

type Schemas = components['schemas']

export type Property = Schemas['Property']
export type PropertyWithDay = Schemas['PropertyWithDay']
export type PropertySettings = Schemas['PropertySettings']
export type CreatePropertyRequest = Schemas['CreatePropertyRequest']
export type PatchPropertyRequest = Schemas['PatchPropertyRequest']
export type DayClock = Schemas['DayClock']
export type BusinessDay = Schemas['BusinessDay']
