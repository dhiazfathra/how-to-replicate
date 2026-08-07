import { expect, test } from '@playwright/test';
import { createWorker, type Worker as OcrWorker } from 'tesseract.js';
import './fixture/api.js';

/** `data:image/png;base64,...` -> a Buffer tesseract.js can OCR directly. */
function pngBufferFromDataUrl(dataUrl: string): Buffer {
  const base64 = dataUrl.slice(dataUrl.indexOf(',') + 1);
  return Buffer.from(base64, 'base64');
}

let ocr: OcrWorker;

test.beforeAll(async () => {
  ocr = await createWorker('eng');
});

test.afterAll(async () => {
  await ocr.terminate();
});

test('nightly: pre-encode video blur survives real record + decode + OCR', async ({ page }) => {
  await page.goto('/');
  await page.waitForFunction(() => Boolean(window.__htrBlurFixture));

  const result = await page.evaluate(() => window.__htrBlurFixture.run());
  expect(result.blurredFrames.length, 'the fixture produced no decoded frames').toBeGreaterThan(0);

  // --- proof the assertion below isn't vacuous ----------------------------
  // If OCR can't read the PHI text even with no blur applied (bad font
  // choice, bad contrast, a broken fixture), a passing "not found" result on
  // the blurred frames would prove nothing.
  const reference = await ocr.recognize(pngBufferFromDataUrl(result.referenceFrame));
  expect(
    reference.data.text,
    'the unblurred reference frame must be OCR-legible, or this test proves nothing',
  ).toContain(result.phiText);

  // --- the actual redaction proof ------------------------------------------
  for (const [index, frame] of result.blurredFrames.entries()) {
    const { data } = await ocr.recognize(pngBufferFromDataUrl(frame));
    expect(
      data.text,
      `blurred frame ${index}: OCR recovered the PHI text from a persisted video frame`,
    ).not.toContain(result.phiText);
  }
});
