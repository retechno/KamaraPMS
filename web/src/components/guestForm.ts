export interface GuestForm {
  first_name: string
  last_name: string
  email: string
  phone: string
  nationality: string
  country_code: string
  date_of_birth: string
  gender: string
  id_type: string
  id_number: string
  address: string
  city: string
  notes: string
}

export const blankGuestForm = (): GuestForm => ({
  first_name: '', last_name: '', email: '', phone: '', nationality: '', country_code: '', date_of_birth: '',
  gender: '', id_type: '', id_number: '', address: '', city: '', notes: '',
})
