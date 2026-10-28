// Records the debug chat walkthrough into $DEMO_TMP/debug-chat.webm, which
// `make demo` turns into docs/debug-chat.gif. Needs telegym-mock on :5678 with echobot
// connected, and Playwright driving the locally installed Chrome.
const { chromium } = require('playwright');

const base = process.env.TELEGYM_MOCK_URL || 'http://localhost:5678';
const tmp = process.env.DEMO_TMP || '.';
const viewport = { width: 880, height: 600 };

(async () => {
  // Headed: headless Chrome swaps ui-monospace for Courier.
  // A fixed scale keeps the recording identical on any monitor.
  const browser = await chromium.launch({
    channel: 'chrome',
    headless: false,
    args: ['--force-device-scale-factor=1'],
  });
  const context = await browser.newContext({
    viewport,
    deviceScaleFactor: 1,
    recordVideo: { dir: tmp + '/video', size: viewport },
  });
  const page = await context.newPage();
  const pause = (ms) => page.waitForTimeout(ms);

  await page.goto(base + '/debug/chat');
  await page.fill('input[name=name]', 'andrii');
  await pause(1200);
  await page.click('text=Open chat');

  const input = page.locator('#composer input[name=text]');
  const replies = page.locator('.kbd-btn', { hasText: 'Echo me' });
  for (const [i, text] of ['/start', 'load testing, but friendly'].entries()) {
    await pause(900);
    await input.pressSequentially(text, { delay: 70 });
    await pause(300);
    await input.press('Enter');
    await replies.nth(i).waitFor();
  }

  await pause(1000);
  await replies.nth(1).click();
  await page.getByText('you clicked').waitFor();
  await pause(2500);

  const video = page.video();
  await context.close();
  await video.saveAs(tmp + '/debug-chat.webm');
  await video.delete();
  await browser.close();
})();
