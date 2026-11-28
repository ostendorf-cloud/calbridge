// Copyright 2026 Simon Ostendorf
// SPDX-License-Identifier: Elastic-2.0

/** metaName is the <meta> tag index.html carries a placeholder for.
 *  internal/landingapi's newStaticHandler substitutes its content
 *  attribute for the deployment's operator, as JSON, once at startup. */
const metaName = 'calbridge-operator'

/** Operator is who the imprint and the privacy policy name. The field
 *  names are the JSON tags of internal/landingconfig.Operator. The five
 *  required fields are always set on a served page, because the server
 *  refuses to start without them. */
export interface Operator {
  name: string
  street: string
  postalCode: string
  city: string
  email: string
  country?: string
  phone?: string
  representative?: string
  register?: string
  vatId?: string
  contentResponsible?: string
  dataProtection?: string
}

const requiredFields = ['name', 'street', 'postalCode', 'city', 'email'] as const
const optionalFields = [
  'country',
  'phone',
  'representative',
  'register',
  'vatId',
  'contentResponsible',
  'dataProtection',
] as const

/** addressLines is the postal address, one line each: the street, the
 *  postal code with the city, and the country where one is set. */
export function addressLines(operator: Operator): string[] {
  const lines = [operator.street, `${operator.postalCode} ${operator.city}`]
  if (operator.country) {
    lines.push(operator.country)
  }
  return lines
}

/** readOperator reads the operator back out of that tag. It returns
 *  undefined when the tag is missing, still holds the unsubstituted
 *  placeholder (the Vite dev server serves index.html as it is), is not
 *  JSON, or lacks a required field. A view then says the details are not
 *  available instead of rendering a page with a blank name. An optional
 *  field that is empty or not a string is left out. */
export function readOperator(doc: Document = document): Operator | undefined {
  const content = doc.querySelector(`meta[name="${metaName}"]`)?.getAttribute('content')
  if (!content) {
    return undefined
  }

  let parsed: unknown
  try {
    parsed = JSON.parse(content)
  } catch {
    return undefined
  }
  if (typeof parsed !== 'object' || parsed === null || Array.isArray(parsed)) {
    return undefined
  }
  const record = parsed as Record<string, unknown>

  const operator: Partial<Operator> = {}
  for (const field of requiredFields) {
    const value = record[field]
    if (typeof value !== 'string' || value.trim() === '') {
      return undefined
    }
    operator[field] = value.trim()
  }
  for (const field of optionalFields) {
    const value = record[field]
    if (typeof value === 'string' && value.trim() !== '') {
      operator[field] = value.trim()
    }
  }

  return operator as Operator
}
