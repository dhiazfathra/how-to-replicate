import { expect, test } from '@playwright/test';
import type { Capture, CaptureState } from '@htr/capture-core';
import './harness/api.js';

/**
 * Invariant 1: `state === 'ready'` is the only gate. A capture in any other
 * state must not surface its document — not its title, not its summary, not a
 * single step — anywhere in the viewer.
 *
 * The seeded documents carry unique marker strings, so "the content is not
 * rendered" is asserted against the exact text that would appear if the gate
 * leaked, rather than against the absence of some element.
 */
const NOT_READY: { state: CaptureState; marker: string }[] = [
  { state: 'failed', marker: 'MARKER-FAILED-4a91c7' },
  { state: 'composing', marker: 'MARKER-COMPOSING-0b3ef2' },
];

test('invariant 1: a capture that is not ready renders none of its document', async ({ page }) => {
  await page.goto('/');
  await page.waitForFunction(() => Boolean(window.__htr));

  const seeded: Capture[] = [];
  for (const { state, marker } of NOT_READY) {
    seeded.push(
      await page.evaluate(
        ([captureState, mark]) =>
          window.__htr.seedCapture({
            state: captureState as CaptureState,
            fidelity: 'degraded',
            withheldEventCount: 7,
            doc: {
              title: `${mark} title`,
              summary: `${mark} summary`,
              steps: [
                { n: 1, text: `${mark} step one`, eventIds: ['evt-1'], tVideo: 1200 },
                { n: 2, text: `${mark} step two`, eventIds: ['evt-2'], tVideo: 2400 },
              ],
              expected: `${mark} expected`,
              actual: `${mark} actual`,
              generator: 'deterministic',
              generatorModel: null,
            },
          }),
        [state, marker] as [string, string],
      ),
    );
  }

  await page.goto('/viewer/');

  const listItems = page.locator('.capture-list button');
  await expect(listItems).toHaveCount(2);

  for (const [index, capture] of seeded.entries()) {
    const marker = NOT_READY[index]!.marker;

    // The list shows the id and the state, never the withheld title.
    await expect(listItems.nth(index)).toHaveText(`${capture.id} (${capture.state})`);

    await listItems.nth(index).click();
    await expect(page.locator('.capture-detail--not-ready')).toHaveText(
      `Capture is not ready (state: ${capture.state}).`,
    );

    // Nothing that renders document content exists on the page.
    await expect(page.locator('.steps')).toHaveCount(0);
    await expect(page.locator('.timeline')).toHaveCount(0);
    await expect(page.locator('.player')).toHaveCount(0);
    await expect(page.locator('[data-testid="player-video"]')).toHaveCount(0);
    // Not even the fidelity badge, which is otherwise unmissable — the gate is
    // on the whole capture, not on individual widgets.
    await expect(page.locator('.fidelity-badge')).toHaveCount(0);

    // And the marker text appears nowhere in the served HTML.
    expect(await page.content(), `${capture.state} capture leaked its document`).not.toContain(
      marker,
    );

    await page.keyboard.press('ControlOrMeta+k');
    const palette = page.getByRole('dialog', { name: 'Command palette' });
    await expect(palette).toBeVisible();
    expect(
      await palette.locator('.command-palette__kind').allTextContents(),
      'the palette must not offer a capture that is not ready',
    ).not.toContain('capture');
    expect(await palette.locator('.command-palette__results').textContent()).not.toContain(marker);
    await palette
      .locator('.command-palette__results li button')
      .filter({ hasText: 'Go to capture list' })
      .click();
    await expect(palette).toBeHidden();
  }
});
