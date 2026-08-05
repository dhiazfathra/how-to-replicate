import { expect, test } from '@playwright/test';
import type { Capture, CaptureEvent, NetworkPayload } from '@htr/capture-core';
import { AUTH_TOKEN, BASE_URL, PHI, bridgeCdpToHarness, reproduceTheBug } from './support.js';

/** `CaptureEvent.payload` is a union keyed by `kind`, with no discriminant field of its own. */
function networkPayload(event: CaptureEvent): NetworkPayload {
  expect(event.kind).toBe('network');
  return event.payload as NetworkPayload;
}

/** Every planted secret must be absent from a persisted capture. */
function expectNoPhi(label: string, serialized: string): void {
  for (const [name, value] of Object.entries({ ...PHI, authToken: AUTH_TOKEN })) {
    expect(serialized, `${label}: raw ${name} survived redaction`).not.toContain(value);
  }
}

/**
 * Guards against the redaction assertion passing vacuously. If the PHI never
 * entered the pipeline in the first place, "no PHI in the output" proves
 * nothing — so assert the engine actually saw it and left its marks.
 */
function expectRedactionActuallyHappened(events: CaptureEvent[]): void {
  const serialized = JSON.stringify(events);
  for (const label of ['mrn', 'email', 'nik', 'dob'] as const) {
    expect(serialized, `expected a [REDACTED:${label}] marker`).toContain(`[REDACTED:${label}]`);
  }

  const network = events.filter((e) => e.kind === 'network');
  expect(network.length, 'no network events were captured').toBeGreaterThan(0);

  const chartCall = network.find((e) => networkPayload(e).url.includes('patient-chart'));
  expect(chartCall, 'the failing chart request was not captured').toBeDefined();
  const chartPayload = networkPayload(chartCall as CaptureEvent);
  expect(chartPayload.url, 'the MRN in the request URL was not redacted').toContain('[REDACTED:mrn]');
  expect(chartPayload.status, 'the demo endpoint should have failed').toBe(500);
  expect(
    Object.values(chartPayload.requestHeaders).join('|'),
    'the authorization header was not stripped',
  ).toContain('[REDACTED]');

  const redacted = events.filter((e) => e.redaction.fidelity === 'redacted');
  expect(redacted.length, 'no event was marked redacted').toBeGreaterThan(0);
  expect(
    new Set(redacted.flatMap((e) => e.redaction.rulesApplied)).size,
    'no redaction rule was recorded as applied',
  ).toBeGreaterThan(1);
}

/** Invariant 3: every event id a step cites must exist in the persisted timeline. */
function expectEveryCitationResolves(capture: Capture, events: CaptureEvent[]): void {
  const known = new Set(events.map((e) => e.id));
  const steps = capture.doc?.steps ?? [];
  expect(steps.length, 'the document has no steps').toBeGreaterThan(3);

  const cited = steps.flatMap((s) => s.eventIds);
  expect(cited.length, 'no step cites any evidence').toBeGreaterThan(0);
  expect(
    cited.filter((id) => !known.has(id)),
    'a step cites an event id that does not exist in the timeline',
  ).toEqual([]);
  expect(steps.map((s) => s.n), 'steps are not numbered 1..n in order').toEqual(
    steps.map((_, i) => i + 1),
  );
}

test('full pipeline: real browser events become a redacted How to Replicate document in the viewer', async ({
  page,
  context,
}) => {
  await page.goto('/');
  await page.waitForFunction(() => Boolean(window.__htr));

  const ruleset = await page.evaluate(() => window.__htr.ruleset());
  expect(ruleset.rules, 'the ruleset must allow-list the origin under test').toContainEqual({
    id: 'origin-allow:e2e',
    class: 'origin-allow',
    origins: [BASE_URL],
  });

  const bridge = await bridgeCdpToHarness(context, page);
  await page.evaluate(() => window.__htr.start());

  await reproduceTheBug(page, bridge, { legacyChart: false });

  const capture = await page.evaluate(() => window.__htr.stop());
  expect(capture.state, 'the capture did not reach ready').toBe('ready');
  expect(capture.fidelity).toBe('full');
  expect(capture.withheldEventCount).toBe(0);
  expect(capture.metadata.rulesetVersion).toBe(ruleset.version);

  const events = await page.evaluate((id) => window.__htr.readEvents(id), capture.id);
  expect(events.length, 'nothing was persisted').toBeGreaterThan(6);

  // --- redaction proof -----------------------------------------------------
  expectRedactionActuallyHappened(events);
  expectNoPhi('persisted events', JSON.stringify(events));
  expectNoPhi('persisted capture record', JSON.stringify(capture));

  // --- invariant 3 ---------------------------------------------------------
  expectEveryCitationResolves(capture, events);

  const stepTexts = (capture.doc?.steps ?? []).map((s) => s.text);
  expect(stepTexts.join('\n')).toMatch(/^Typed into "Medical record number" on http/m);
  expect(stepTexts.join('\n')).toMatch(/^Clicked "Open chart for \[REDACTED:mrn\]" on http/m);
  expect(stepTexts.at(-1), 'the last step should be the hash navigation').toMatch(
    /^Navigated to http.*#\/billing$/,
  );

  // --- the viewer, reading the same IndexedDB on the same origin -----------
  await page.goto('/viewer/');

  const listItem = page.locator('.capture-list button');
  await expect(listItem).toHaveCount(1);
  await expect(listItem).toHaveText(capture.doc?.title ?? '');
  await listItem.click();

  await expect(page.locator('.capture-detail h1')).toHaveText(capture.doc?.title ?? '');
  // Invariant 4, negative case: a full-fidelity capture shows no badge.
  await expect(page.locator('.fidelity-badge')).toHaveCount(0);
  await expect(page.locator('.player--empty')).toHaveText('No video for this capture');

  const renderedSteps = page.locator('.steps li');
  await expect(renderedSteps).toHaveCount(stepTexts.length);
  for (const [index, text] of stepTexts.entries()) {
    await expect(renderedSteps.nth(index)).toContainText(text);
  }
  await expect(page.locator('.timeline__event')).toHaveCount(events.length);

  // Invariant 3, rendered: clicking a step highlights exactly the timeline
  // rows for the event ids it cites — proof the citations resolve in the UI,
  // not just in the persisted JSON.
  for (const [index, step] of (capture.doc?.steps ?? []).entries()) {
    await renderedSteps.nth(index).locator('button').click();
    const highlighted = await page
      .locator('.timeline__event--highlighted')
      .evaluateAll((nodes) => nodes.map((n) => n.getAttribute('data-event-id')));
    expect(highlighted.slice().sort(), `step ${step.n} highlighted the wrong events`).toEqual(
      step.eventIds.slice().sort(),
    );
  }

  // Nothing PHI-shaped reached the rendered page either.
  expectNoPhi('rendered viewer DOM', await page.content());

  // --- command palette, in a real browser ----------------------------------
  await page.keyboard.press('ControlOrMeta+k');
  const palette = page.getByRole('dialog', { name: 'Command palette' });
  await expect(palette).toBeVisible();

  await palette.getByLabel('Search').fill('go to capture');
  await expect(palette.locator('.command-palette__results li')).toHaveCount(1);
  await palette.locator('.command-palette__results li button').click();
  await expect(palette).toBeHidden();
  await expect(listItem).toHaveCount(1); // the action routed back to the list

  await page.keyboard.press('ControlOrMeta+k');
  await expect(palette).toBeVisible();
  await palette.getByLabel('Search').fill(capture.id.slice(0, 8).toLowerCase());
  await palette.locator('.command-palette__results li button').first().click();
  await expect(page.locator('.capture-detail h1')).toHaveText(capture.doc?.title ?? '');
});

test('invariant 4: a redaction drop degrades the capture and the viewer says so', async ({
  page,
  context,
}) => {
  await page.goto('/');
  await page.waitForFunction(() => Boolean(window.__htr));

  const bridge = await bridgeCdpToHarness(context, page);
  await page.evaluate(() => window.__htr.start());

  // The legacy endpoint sends `patient` as a bare string, so the ruleset's
  // `/patient/mrn` pointer lands on a scalar. The engine cannot redact what it
  // cannot resolve, so it fails closed and drops the whole event.
  await reproduceTheBug(page, bridge, { legacyChart: true });

  const buffered = await page.evaluate(() => window.__htr.stats());
  expect(buffered.withheld, 'the legacy request should have been dropped').toBeGreaterThan(0);

  const capture = await page.evaluate(() => window.__htr.stop());
  expect(capture.state).toBe('ready');
  expect(capture.fidelity).toBe('degraded');
  expect(capture.withheldEventCount).toBe(buffered.withheld);

  const events = await page.evaluate((id) => window.__htr.readEvents(id), capture.id);
  expectNoPhi('persisted events', JSON.stringify(events));
  expect(
    events.some(
      (e) => e.kind === 'network' && (e.payload as NetworkPayload).url.includes('patient-chart'),
    ),
    'the dropped request must not be persisted at all',
  ).toBe(false);

  await page.goto('/viewer/');
  await page.locator('.capture-list button').click();

  const badge = page.getByRole('status');
  await expect(badge).toBeVisible();
  await expect(badge.locator('.fidelity-badge__label')).toHaveText('Degraded capture');
  await expect(badge.locator('.fidelity-badge__count')).toHaveText(
    `${capture.withheldEventCount} events withheld by redaction policy`,
  );
});
