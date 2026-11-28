// Copyright 2026 Simon Ostendorf
// SPDX-License-Identifier: Elastic-2.0

import { afterEach, describe, expect, it } from 'vitest'

import { addressLines, readOperator } from './operator'

const complete = {
  name: 'Erika Mustermann',
  street: 'Musterstrasse 12',
  postalCode: '10115',
  city: 'Berlin',
  email: 'kontakt@calbridge.example',
}

function setOperatorMeta(content: string): void {
  document.head.querySelector('meta[name="calbridge-operator"]')?.remove()
  const meta = document.createElement('meta')
  meta.setAttribute('name', 'calbridge-operator')
  meta.setAttribute('content', content)
  document.head.appendChild(meta)
}

describe('addressLines', () => {
  it('lists the street and the postal code with the city', () => {
    expect(addressLines(complete)).toEqual(['Musterstrasse 12', '10115 Berlin'])
  })

  it('adds the country when one is set', () => {
    expect(addressLines({ ...complete, country: 'Deutschland' })).toEqual([
      'Musterstrasse 12',
      '10115 Berlin',
      'Deutschland',
    ])
  })
})

describe('readOperator', () => {
  afterEach(() => {
    document.head.querySelector('meta[name="calbridge-operator"]')?.remove()
  })

  it('returns undefined when the page has no operator tag', () => {
    expect(readOperator()).toBeUndefined()
  })

  it('returns undefined for the unsubstituted placeholder', () => {
    setOperatorMeta('__CALBRIDGE_OPERATOR__')

    expect(readOperator()).toBeUndefined()
  })

  it('returns undefined for an empty attribute', () => {
    setOperatorMeta('')

    expect(readOperator()).toBeUndefined()
  })

  it.each([
    ['a JSON array', '[]'],
    ['a JSON string', '"Erika Mustermann"'],
    ['JSON null', 'null'],
  ])('returns undefined for %s', (_name, content) => {
    setOperatorMeta(content)

    expect(readOperator()).toBeUndefined()
  })

  it.each(['name', 'street', 'postalCode', 'city', 'email'])(
    'returns undefined when the required field %s is missing',
    (field) => {
      const without: Record<string, string> = { ...complete }
      delete without[field]
      setOperatorMeta(JSON.stringify(without))

      expect(readOperator()).toBeUndefined()
    },
  )

  it('returns undefined when a required field is blank', () => {
    setOperatorMeta(JSON.stringify({ ...complete, city: '   ' }))

    expect(readOperator()).toBeUndefined()
  })

  it('returns undefined when a required field is not a string', () => {
    setOperatorMeta(JSON.stringify({ ...complete, postalCode: 10115 }))

    expect(readOperator()).toBeUndefined()
  })

  it('returns the required fields alone when nothing else is set', () => {
    setOperatorMeta(JSON.stringify(complete))

    expect(readOperator()).toEqual(complete)
  })

  it('keeps the optional fields that are set and drops the ones that are not', () => {
    setOperatorMeta(
      JSON.stringify({
        ...complete,
        phone: '+49 30 1234567',
        vatId: '',
        register: 42,
        dataProtection: 'datenschutz@calbridge.example',
      }),
    )

    expect(readOperator()).toEqual({
      ...complete,
      phone: '+49 30 1234567',
      dataProtection: 'datenschutz@calbridge.example',
    })
  })

  it('ignores a field the contract does not name', () => {
    setOperatorMeta(JSON.stringify({ ...complete, admin: 'yes' }))

    expect(readOperator()).toEqual(complete)
  })

  it('reads the attribute back the way the server escapes it', () => {
    // The server writes html.EscapeString output into the attribute. The
    // browser unescapes it before getAttribute returns, so the JSON the
    // tag held is what readOperator parses.
    const host = document.createElement('div')
    host.innerHTML =
      '<meta name="calbridge-operator" content="{&#34;name&#34;:&#34;Erika &amp; Sohn&#34;,' +
      '&#34;street&#34;:&#34;Musterstrasse 12&#34;,&#34;postalCode&#34;:&#34;10115&#34;,' +
      '&#34;city&#34;:&#34;Berlin&#34;,&#34;email&#34;:&#34;kontakt@calbridge.example&#34;}">'
    document.head.appendChild(host.firstElementChild as Element)

    expect(readOperator()?.name).toBe('Erika & Sohn')
  })
})
