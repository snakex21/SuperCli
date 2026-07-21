"use strict";

const { chromium } = require("playwright");

const [superCliURL, nestCafeURL] = process.argv.slice(2);
if (!superCliURL || !nestCafeURL) {
  throw new Error("usage: node scripts/test-ui-scaling.cjs SUPERCLI_URL NESTCAFE_URL");
}

const scales = ["compact", "normal", "large", "xlarge", "huge", "auto"];
const viewports = [
  { width: 1280, height: 720 },
  { width: 900, height: 620 },
];

async function saveSetting(page, key, value) {
  await page.evaluate(
    async ({ key, value }) => {
      const response = await fetch("/api/settings", {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ [key]: value }),
      });
      if (!response.ok) throw new Error(await response.text());
    },
    { key, value },
  );
}

async function assertSurface(browser, spec) {
  for (const viewport of viewports) {
    const page = await browser.newPage({ viewport });
    if (spec.modelDialog) {
      await page.route("**/api/models", (route) => route.fulfill({
        contentType: "application/json",
        body: JSON.stringify({
          active: "test-model-01",
          provider: "test-provider",
          models: Array.from({ length: 80 }, (_, index) => ({
            id: `test-model-${String(index + 1).padStart(2, "0")}`,
            provider: "test-provider",
            tool_use: true,
          })),
        }),
      }));
    }
    await page.goto(spec.url, { waitUntil: "domcontentloaded" });
    for (const scale of scales) {
      await saveSetting(page, spec.key, scale);
      await page.reload({ waitUntil: "domcontentloaded" });
      await page.waitForFunction(
        ({ scale, attribute }) => document.documentElement.getAttribute(attribute) === scale,
        { scale, attribute: spec.attribute },
      );
      await page.evaluate(() => document.getAnimations().forEach((animation) => animation.finish()));
      const geometry = await page.evaluate(({ composer, send }) => {
        const composerRect = document.querySelector(composer).getBoundingClientRect();
        const sendRect = document.querySelector(send).getBoundingClientRect();
        const workspaceRect = document.querySelector(".workspace")?.getBoundingClientRect();
        const welcomeRect = document.querySelector(".welcome-intro")?.getBoundingClientRect();
        return {
          innerWidth: window.innerWidth,
          innerHeight: window.innerHeight,
          composer: composerRect.toJSON(),
          send: sendRect.toJSON(),
          workspace: workspaceRect?.toJSON(),
          welcome: welcomeRect?.toJSON(),
          zoom: Number.parseFloat(document.documentElement.style.zoom || "1"),
        };
      }, spec);
      const tolerance = 2;
      if (
        geometry.composer.left < -tolerance ||
        geometry.composer.right > geometry.innerWidth + tolerance ||
        geometry.composer.bottom > geometry.innerHeight + tolerance ||
        geometry.send.left < -tolerance ||
        geometry.send.right > geometry.innerWidth + tolerance ||
        geometry.send.bottom > geometry.innerHeight + tolerance
      ) {
        throw new Error(`${spec.name} ${viewport.width}x${viewport.height} ${scale}: ${JSON.stringify(geometry)}`);
      }
      if (spec.centeredEmptyState) {
        const groupCenterY = (geometry.welcome.top + geometry.composer.bottom) / 2;
        const workspaceCenterY = (geometry.workspace.top + geometry.workspace.bottom) / 2;
        const composerCenterX = (geometry.composer.left + geometry.composer.right) / 2;
        const workspaceCenterX = (geometry.workspace.left + geometry.workspace.right) / 2;
        const centerTolerance = Math.max(4, geometry.zoom * 3);
        if (
          Math.abs(groupCenterY - workspaceCenterY) > centerTolerance ||
          Math.abs(composerCenterX - workspaceCenterX) > centerTolerance
        ) {
          throw new Error(`${spec.name} centered home ${viewport.width}x${viewport.height} ${scale}: ${JSON.stringify({ groupCenterY, workspaceCenterY, composerCenterX, workspaceCenterX, geometry })}`);
        }
      }
      if (spec.settingsDialog) {
        await page.evaluate(() => window.openNestCafeSettingsPage("general"));
        if (await page.locator('[aria-label="Ostrzegaj przy aktywnym zadaniu"]').count()) {
          throw new Error(`${spec.name} still exposes the redundant close-confirmation setting`);
        }
        const fullBackup = page.locator('input[name="backup-scope"][value="full"]');
        const safeBackup = page.locator('input[name="backup-scope"][value="safe"]');
        await fullBackup.waitFor({ state: "attached" });
        await safeBackup.check();
        await page.waitForFunction(() => document.querySelector('[data-backup-scope="safe"][data-export-path="/api/data/export"]'));
        if (await page.locator(".settings-backup-scope .excluded").count() !== 1) {
          throw new Error(`${spec.name} safe backup did not exclude credentials`);
        }
        await fullBackup.check();
        await page.waitForFunction(() => document.querySelector('[data-backup-scope="full"][data-export-path="/api/data/export/full"]'));
        if (await page.locator(".settings-backup-scope .excluded").count() !== 0) {
          throw new Error(`${spec.name} full backup did not include every category`);
        }
        const scaleControl = page.locator('select[aria-label="Skala interfejsu"]');
        await scaleControl.waitFor({ state: "visible" });
        await scaleControl.scrollIntoViewIfNeeded();
        const settingsGeometry = await page.evaluate((selector) => {
          const dialog = document.querySelector(selector).getBoundingClientRect();
          const control = document.querySelector('select[aria-label="Skala interfejsu"]').getBoundingClientRect();
          return { innerWidth, innerHeight, dialog: dialog.toJSON(), control: control.toJSON() };
        }, spec.settingsDialog);
        const dialogTolerance = 8;
        if (
          settingsGeometry.dialog.right > settingsGeometry.innerWidth + dialogTolerance ||
          settingsGeometry.dialog.bottom > settingsGeometry.innerHeight + dialogTolerance ||
          settingsGeometry.control.right > settingsGeometry.innerWidth + tolerance ||
          settingsGeometry.control.bottom > settingsGeometry.innerHeight + tolerance
        ) {
          throw new Error(`${spec.name} settings ${viewport.width}x${viewport.height} ${scale}: ${JSON.stringify(settingsGeometry)}`);
        }
        await page.evaluate((selector) => document.querySelector(selector).close(), spec.settingsDialog);

		await page.evaluate(() => window.openNestCafeSettingsPage("folders"));
		await page.getByText("Analiza dokumentów przez AI", { exact: true }).waitFor({ state: "visible" });
		await page.getByText("Poczta Outlook", { exact: true }).waitFor({ state: "visible" });
		const outlookFolder = page.locator(".folder-outlook-name");
		await outlookFolder.scrollIntoViewIfNeeded();
		const sourceGeometry = await page.evaluate((selector) => {
		  const dialog = document.querySelector(selector).getBoundingClientRect();
		  const control = document.querySelector(".folder-outlook-name").getBoundingClientRect();
		  return { innerWidth, innerHeight, dialog: dialog.toJSON(), control: control.toJSON() };
		}, spec.settingsDialog);
		if (
		  sourceGeometry.dialog.right > sourceGeometry.innerWidth + dialogTolerance ||
		  sourceGeometry.dialog.bottom > sourceGeometry.innerHeight + dialogTolerance ||
		  sourceGeometry.control.right > sourceGeometry.innerWidth + tolerance ||
		  sourceGeometry.control.bottom > sourceGeometry.innerHeight + tolerance
		) {
		  throw new Error(`${spec.name} document sources ${viewport.width}x${viewport.height} ${scale}: ${JSON.stringify(sourceGeometry)}`);
		}
		await page.evaluate((selector) => document.querySelector(selector).close(), spec.settingsDialog);
      }
      if (spec.modelDialog) {
        // Stan po wysłaniu pierwszej wiadomości: kompozytor jest przy dolnej
        // krawędzi, więc menu musi otworzyć się nad nim i pozostać przewijalne.
        await page.evaluate(() => document.body.classList.remove("empty-state"));
        await page.locator("#model-button").click();
        await page.locator("#model-list .model-row").first().waitFor({ state: "visible" });
        const lastModel = page.locator("#model-list .model-row").last();
        await lastModel.scrollIntoViewIfNeeded();
        const modelGeometry = await page.evaluate((selector) => {
          const dialog = document.querySelector(selector).getBoundingClientRect();
          const list = document.querySelector("#model-list").getBoundingClientRect();
          const last = document.querySelector("#model-list .model-row:last-child").getBoundingClientRect();
          return {
            innerWidth,
            innerHeight,
            dialog: dialog.toJSON(),
            list: list.toJSON(),
            last: last.toJSON(),
          };
        }, spec.modelDialog);
        if (
          modelGeometry.dialog.left < -tolerance ||
          modelGeometry.dialog.right > modelGeometry.innerWidth + tolerance ||
          modelGeometry.dialog.top < -tolerance ||
          modelGeometry.dialog.bottom > modelGeometry.innerHeight + tolerance ||
          modelGeometry.last.top < modelGeometry.list.top - tolerance ||
          modelGeometry.last.bottom > modelGeometry.list.bottom + tolerance
        ) {
          throw new Error(`${spec.name} model menu ${viewport.width}x${viewport.height} ${scale}: ${JSON.stringify(modelGeometry)}`);
        }
        await page.evaluate((selector) => document.querySelector(selector).close(), spec.modelDialog);
      }
      process.stdout.write(`PASS ${spec.name} ${viewport.width}x${viewport.height} ${scale} zoom=${geometry.zoom}\n`);
    }
    await page.close();
  }
}

(async () => {
  const executablePath = process.env.PLAYWRIGHT_BROWSER_PATH;
  const browser = await chromium.launch({ headless: true, ...(executablePath ? { executablePath } : {}) });
  try {
    await assertSurface(browser, {
      name: "SuperCli",
      url: superCliURL,
      key: "ui.uiScale",
      attribute: "data-ui-scale",
      composer: "#composer",
      send: "#send-btn",
    });
    await assertSurface(browser, {
      name: "NestCafe",
      url: nestCafeURL,
      key: "ui.scale",
      attribute: "data-scale",
      composer: "#composer",
      send: "#send-button",
      settingsDialog: "#settings-page-dialog",
      modelDialog: "#model-dialog",
      centeredEmptyState: true,
    });
  } finally {
    await browser.close();
  }
})().catch((error) => {
  console.error(error);
  process.exitCode = 1;
});
