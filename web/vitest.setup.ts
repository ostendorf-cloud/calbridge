// jest-dom's matchers, so an assertion reads as what a user would see:
// toBeVisible, toHaveAccessibleName, toBeDisabled.
import '@testing-library/jest-dom/vitest'

import { cleanup } from '@testing-library/vue'
import { afterEach } from 'vitest'

// Testing Library only registers this itself when Vitest runs with globals, and
// this package does not: without it one test finds the elements of the
// previous one.
afterEach(cleanup)

/*
 * jsdom has no layout engine: every element reports a zero size no matter
 * what its CSS says, and it defines no ResizeObserver at all. Vue Flow's
 * canvas (CanvasView) measures every node with both before it flips that
 * node's visibility on, so without the polyfills below a node the canvas
 * renders in a test stays invisible forever.
 *
 * offsetWidth and offsetHeight are patched to read back the inline
 * width/height a node carries (CanvasView always sets both, since its own
 * layout is computed, not browser-measured), falling back to a plausible
 * non-zero box for anything Vue Flow measures that this app did not size
 * itself, such as a connection handle or an edge label.
 */
function pixelsOf(value: string, fallback: number): number {
  const parsed = Number.parseFloat(value)
  return Number.isFinite(parsed) && parsed > 0 ? parsed : fallback
}

function rectOf(width: number, height: number): DOMRect {
  return {
    width,
    height,
    top: 0,
    left: 0,
    right: width,
    bottom: height,
    x: 0,
    y: 0,
    toJSON() {
      return this
    },
  }
}

Object.defineProperty(HTMLElement.prototype, 'offsetWidth', {
  configurable: true,
  get(this: HTMLElement): number {
    return pixelsOf(this.style.width, 24)
  },
})

Object.defineProperty(HTMLElement.prototype, 'offsetHeight', {
  configurable: true,
  get(this: HTMLElement): number {
    return pixelsOf(this.style.height, 24)
  },
})

HTMLElement.prototype.getBoundingClientRect = function (this: HTMLElement): DOMRect {
  return rectOf(this.offsetWidth, this.offsetHeight)
}

// Vue Flow's click-to-connect path (completing a connection with two clicks
// on two handles rather than a drag) asks the document what sits under the
// pointer with elementFromPoint, to tell a handle from empty canvas. jsdom
// has no layout engine and does not implement the method at all, so without
// this stub every click on a handle throws instead of completing the
// connection. Returning null is correct for this app regardless, since a
// click's own target element (read from the event instead) is what every
// handle lookup here actually falls back to.
if (typeof Document.prototype.elementFromPoint === 'undefined') {
  Document.prototype.elementFromPoint = (): Element | null => null
}

// Vue Flow's edge label measures its own <text> node with getBBox.
// jsdom's SVGElement does not carry it at all, and typing it here through
// global augmentation is what makes the assignment below honest rather
// than a cast: this file is the one place in the app that actually adds
// the method.
declare global {
  interface SVGElement {
    getBBox(): DOMRect
  }
}

SVGElement.prototype.getBBox = function (): DOMRect {
  return rectOf(50, 16)
}

// Vue Flow reads the viewport's zoom factor off a DOMMatrixReadOnly built
// from its own computed transform, and jsdom implements no CSSOM matrix
// type at all. Every transform this app's nodes carry is a plain
// translate(), never a scale(), so the identity matrix this stub returns
// (m22, the only field Vue Flow reads, stays 1) is exactly right for a test.
class DOMMatrixReadOnlyStub {
  readonly m22 = 1
}

if (typeof globalThis.DOMMatrixReadOnly === 'undefined') {
  globalThis.DOMMatrixReadOnly = DOMMatrixReadOnlyStub as unknown as typeof DOMMatrixReadOnly
}

class ResizeObserverStub implements ResizeObserver {
  constructor(private readonly callback: ResizeObserverCallback) {}

  observe(target: Element): void {
    const entry = { target } as unknown as ResizeObserverEntry
    queueMicrotask(() => this.callback([entry], this))
  }

  unobserve(): void {}
  disconnect(): void {}
}

if (typeof globalThis.ResizeObserver === 'undefined') {
  globalThis.ResizeObserver = ResizeObserverStub
}

// Reka UI's popper (behind GovueSelect's dropdown) tracks its anchor with
// an IntersectionObserver, which jsdom defines no more than it does
// ResizeObserver. Every anchor is "intersecting" here: there is no
// viewport to scroll it out of.
class IntersectionObserverStub implements IntersectionObserver {
  readonly root: Element | Document | null = null
  readonly rootMargin: string = ''
  readonly thresholds: ReadonlyArray<number> = []

  constructor(private readonly callback: IntersectionObserverCallback) {}

  observe(target: Element): void {
    const entry = { target, isIntersecting: true } as unknown as IntersectionObserverEntry
    queueMicrotask(() => this.callback([entry], this))
  }

  unobserve(): void {}
  disconnect(): void {}
  takeRecords(): IntersectionObserverEntry[] {
    return []
  }
}

if (typeof globalThis.IntersectionObserver === 'undefined') {
  globalThis.IntersectionObserver = IntersectionObserverStub
}
