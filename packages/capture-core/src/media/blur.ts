/**
 * Pre-encode frame-blur compositing. Originally written for the extension's
 * offscreen recorder (Task 12); moved here in Task 14 because it has no
 * `chrome.*`/extension-specific dependency — it is pure canvas/rect math — so
 * `clients/extension` and `clients/recording-link` can share one
 * implementation instead of two copies that can drift on a compliance-
 * critical routine. `clients/extension/src/offscreen/blur.ts` re-exports
 * this module rather than duplicating it.
 */

/** Structural rect shape shared by DOM `getBoundingClientRect()` and any other rect source. */
export type RectLike = { x: number; y: number; width: number; height: number };

/** Blur radius applied inside each region. Fixed — not worth a config surface for Phase 0. */
export const BLUR_PX = 24;

/** Structural subset of `CanvasRenderingContext2D`/`OffscreenCanvasRenderingContext2D` this module draws through. */
export type DrawTarget = {
  filter: string;
  drawImage(source: unknown, dx: number, dy: number, dw: number, dh: number): void;
  save(): void;
  restore(): void;
  beginPath(): void;
  rect(x: number, y: number, w: number, h: number): void;
  clip(): void;
};

/**
 * Validate raw blur regions before they ever touch a frame. Any malformed
 * rect (non-finite or negative) is a resolution failure — returning `null`
 * here is the signal the recorder uses to stop rather than draw an
 * unblurred frame (invariant 2).
 */
export function validateRegions(regions: readonly RectLike[]): RectLike[] | null {
  for (const r of regions) {
    if (
      !Number.isFinite(r.x) ||
      !Number.isFinite(r.y) ||
      !Number.isFinite(r.width) ||
      !Number.isFinite(r.height) ||
      r.width < 0 ||
      r.height < 0
    ) {
      return null;
    }
  }
  return regions.map((r) => ({ ...r }));
}

/**
 * Composite one frame: draw the source frame in full, then redraw each
 * validated rect on top through a clip with `filter: blur(...)`. The
 * unblurred draw always happens first and underneath — every pixel inside a
 * rect ends up covered by the blurred redraw, never the reverse.
 */
export function compositeFrame(
  ctx: DrawTarget,
  source: unknown,
  width: number,
  height: number,
  rects: readonly RectLike[],
): void {
  ctx.filter = 'none';
  ctx.drawImage(source, 0, 0, width, height);

  for (const rect of rects) {
    ctx.save();
    ctx.beginPath();
    ctx.rect(rect.x, rect.y, rect.width, rect.height);
    ctx.clip();
    ctx.filter = `blur(${BLUR_PX}px)`;
    ctx.drawImage(source, 0, 0, width, height);
    ctx.restore();
  }
}
