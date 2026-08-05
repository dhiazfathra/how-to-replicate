import fs from 'node:fs';
import path from 'node:path';
import { fileURLToPath } from 'node:url';

const here = path.dirname(fileURLToPath(import.meta.url));
const RESULTS_DIR = path.join(here, 'artifacts/test-results');
const VIDEO_DIR = path.join(here, 'artifacts/videos');

/**
 * Playwright writes each video into a per-test subdirectory of `outputDir`,
 * which is gitignored noise. Lift them out to one stable, committed folder so
 * the recorded evidence has a path that does not change between runs.
 *
 * Off by default: `artifacts/videos/` is committed evidence, and refreshing it
 * on every local run leaves a dirty tree of binary churn (and used to delete
 * the hand-built `evidence-reel.gif` outright). CI sets `HTR_REFRESH_EVIDENCE`
 * so its uploaded artifact holds that run's recordings; a developer
 * deliberately re-cutting the committed evidence sets it too.
 */
export default function collectEvidence(): void {
  if (!process.env.HTR_REFRESH_EVIDENCE) return;
  if (!fs.existsSync(RESULTS_DIR)) return;
  fs.mkdirSync(VIDEO_DIR, { recursive: true });

  for (const entry of fs.readdirSync(RESULTS_DIR, { withFileTypes: true })) {
    if (!entry.isDirectory()) continue;
    const dir = path.join(RESULTS_DIR, entry.name);
    const videos = fs.readdirSync(dir).filter((f) => f.endsWith('.webm'));
    videos.forEach((video, index) => {
      const suffix = videos.length > 1 ? `-${index + 1}` : '';
      fs.copyFileSync(path.join(dir, video), path.join(VIDEO_DIR, `${entry.name}${suffix}.webm`));
    });
  }
}
