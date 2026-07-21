"use strict";

const { chromium } = require("playwright");

const baseURL = process.argv[2] || "http://127.0.0.1:19881";

function assert(condition, message) {
  if (!condition) throw new Error(message);
}

let browser;
(async () => {
  browser = await chromium.launch({
    headless: true,
    executablePath: "C:\\Program Files\\Google\\Chrome\\Application\\chrome.exe",
  });
  const page = await browser.newPage({ viewport: { width: 790, height: 520 }, colorScheme: "dark" });
  const models = Array.from({ length: 34 }, (_, index) => ({
    id: `catalog-model-${String(index + 1).padStart(2, "0")}`,
    provider: "any-router",
    active: index === 0,
    context_length: 100000,
  }));
  models.push({ id: "gpt-5-codex", provider: "any-router", context_length: 400000 });
  models.push({ id: "gpt-5.6-sol", provider: "any-router", context_length: 1000000, reasoning: true });

  await page.route("**/api/models", (route) => route.fulfill({
    status: 200,
    contentType: "application/json",
    body: JSON.stringify({ active: models[0].id, provider: "any-router", models }),
  }));
  await page.goto(baseURL, { waitUntil: "networkidle" });
  await page.locator("#model-btn").click();
  const palette = page.locator("#palette");
  const list = page.locator("#model-list");
  const lastModel = page.locator(".prow", { hasText: "gpt-5.6-sol" });
  await lastModel.waitFor({ state: "attached" });

  const dimensions = await list.evaluate((node) => ({ clientHeight: node.clientHeight, scrollHeight: node.scrollHeight }));
  assert(dimensions.clientHeight > 0, "lista modeli nie ma dostępnej wysokości");
  assert(dimensions.scrollHeight > dimensions.clientHeight, "długa lista modeli nie jest przewijalna");
  await list.evaluate((node) => { node.scrollTop = node.scrollHeight; });
  await page.waitForTimeout(120);

  const viewport = page.viewportSize();
  const paletteBox = await palette.boundingBox();
  const listBox = await list.boundingBox();
  const lastBox = await lastModel.boundingBox();
  assert(viewport && paletteBox && paletteBox.y + paletteBox.height <= viewport.height + 1, "paleta wychodzi poza małe okno");
  assert(listBox && lastBox && lastBox.y >= listBox.y - 1, "ostatni model jest ponad obszarem listy");
  assert(lastBox.y + lastBox.height <= listBox.y + listBox.height + 1, "ostatni model jest ucięty pod listą");
  assert(await lastModel.isVisible(), "gpt-5.6-sol nie jest widoczny po przewinięciu");
  await page.screenshot({ path: "screenshots/model-palette-small-window.png" });
  process.stdout.write(`PASS model palette: ${dimensions.clientHeight}px viewport list, gpt-5.6-sol reachable\n`);
})().finally(async () => {
  await browser?.close();
}).catch((error) => {
  console.error(error);
  process.exitCode = 1;
});
