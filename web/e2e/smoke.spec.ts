import { expect, test } from "@playwright/test";

const PASSWORD = "correct-horse-battery-staple";

test("setup, issue a certificate and download it", async ({ page }) => {
  await page.goto("/setup");
  await page.getByLabel("Setup Token").fill("e2e-token");
  await page.getByLabel("邮箱").fill("admin@example.com");
  await page.getByLabel("密码", { exact: true }).fill(PASSWORD);
  await page.getByLabel("确认密码").fill(PASSWORD);
  await page.getByRole("button", { name: "完成初始化" }).click();
  await expect(page).toHaveURL(/\/$/);
  await expect(page.locator("html")).not.toHaveClass(/dark/);

  // CA account.
  await page.goto("/ca-accounts");
  await page.getByRole("button", { name: "添加 CA" }).first().click();
  await page.getByLabel("快速选择").click();
  await page.getByRole("option", { name: "Let's Encrypt", exact: true }).click();
  await expect(page.getByLabel("邮箱").last()).toHaveValue("admin@example.com");
  await page.getByLabel("邮箱").last().fill("ca-ops@example.com");
  await page.getByRole("switch", { name: "我已阅读并同意 CA 的服务条款" }).click();
  await page.getByRole("button", { name: "创建", exact: true }).click();
  await expect(page.getByRole("button", { name: "注册账户" })).toBeVisible();

  // The last CA email is remembered for the next account.
  await page.getByRole("button", { name: "添加 CA" }).first().click();
  await expect(page.getByLabel("邮箱").last()).toHaveValue("ca-ops@example.com");
  await page.getByRole("button", { name: "取消" }).click();

  await page.getByRole("button", { name: "注册账户" }).click();
  await expect(page.locator("tbody")).toContainText("已注册", { timeout: 10_000 });

  // DNS credential.
  await page.goto("/dns-credentials");
  await page.getByRole("button", { name: "添加凭据" }).first().click();
  await page.getByLabel("名称").fill("CF E2E");
  await page.getByLabel("API Token").fill("fake-token");
  await page.getByRole("button", { name: "保存" }).click();
  await expect(page.locator("tbody")).toContainText("CF E2E");

  // Certificate wizard.
  await page.goto("/certificates/new");
  await page.getByLabel("CA 账户").click();
  await page.getByRole("listbox").getByRole("option").nth(1).click();
  await page.getByRole("button", { name: "下一步" }).click();
  await page.getByLabel("主域名").fill("example.com");
  await page.getByLabel("附加域名 (SAN)").fill("www.example.com");
  await page.getByRole("button", { name: "下一步" }).click();
  await page.getByLabel("DNS 凭据").click();
  await page.getByRole("listbox").getByRole("option").nth(1).click();
  await page.getByRole("button", { name: "下一步" }).click();
  await page.getByRole("button", { name: "下一步" }).click();
  await page.getByRole("button", { name: "创建" }).click();

  await expect(page).toHaveURL(/\/certificates\/[A-Z0-9]+/);
  await expect(page.getByText("下次续期")).toBeVisible({ timeout: 20_000 });
  await expect(page.getByText("历史版本 (1)")).toBeVisible({ timeout: 10_000 });

  page.on("dialog", (dialog) => dialog.accept());
  const downloadPromise = page.waitForEvent("download");
  await page.getByRole("button", { name: "ZIP", exact: true }).click();
  const download = await downloadPromise;
  expect(download.suggestedFilename()).toContain(".zip");

  // Live job log.
  await page.goto("/jobs");
  await page.getByRole("link", { name: "详情" }).first().click();
  await expect(page.getByText("=== job", { exact: false })).toBeVisible({ timeout: 10_000 });
});

test("rejects protected routes without a session", async ({ page }) => {
  await page.goto("/certificates");
  await expect(page).toHaveURL(/\/login$/);
});
