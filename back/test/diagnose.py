#!/usr/bin/env python3
"""Peerdrive 前端诊断 — Playwright 真实浏览器"""
import asyncio, json, sys
from playwright.async_api import async_playwright

BASE = "http://127.0.0.1:5173"
API = "http://127.0.0.1:3000"

async def main():
    results = []
    async with async_playwright() as p:
        browser = await p.chromium.launch(headless=True)
        ctx = await browser.new_context(viewport={"width": 1440, "height": 900})
        page = await ctx.new_page()
        errors = []

        page.on("console", lambda msg: errors.append(f"[{msg.type}] {msg.text}") if msg.type in ("error","warning") else None)
        page.on("pageerror", lambda err: errors.append(f"[PAGE] {err}"))

        # ── 1. Plaza (首页) ──
        await page.goto(f"{BASE}/", timeout=15000, wait_until="networkidle")
        await page.wait_for_timeout(1000)
        r1 = {
            "page": "Plaza (/)",
            "url": page.url,
            "status": "ok" if await page.title() else "fail",
            "body_overflow": await page.evaluate("""() => {
                const b = document.body;
                return {scrollHeight: b.scrollHeight, clientHeight: b.clientHeight,
                        overflowing: b.scrollHeight > b.clientHeight + 5}
            }"""),
            "collection_cards": await page.evaluate("""() =>
                document.querySelectorAll('[class*=\"rounded-xl\"]').length
            """),
            "errors": [e for e in errors if "favicon" not in e.lower()],
        }
        errors.clear()
        results.append(r1)

        # ── 2. FileManager ──
        await page.goto(f"{BASE}/files", timeout=15000, wait_until="networkidle")
        await page.wait_for_timeout(1500)
        checkboxes = await page.evaluate("""() => {
            const cbs = document.querySelectorAll('input[type="checkbox"]');
            return {count: cbs.length, visible: Array.from(cbs).filter(c => c.offsetParent !== null).length}
        }""")
        r2 = {
            "page": "FileManager (/files)",
            "status": "ok" if await page.title() else "fail",
            "checkboxes": checkboxes,
            "body_overflow": await page.evaluate("""() => {
                const main = document.querySelector('[class*=\"overflow-y-auto\"]');
                if (!main) return {found: false};
                return {scrollHeight: main.scrollHeight, clientHeight: main.clientHeight,
                        overflowing: main.scrollHeight > main.clientHeight + 5}
            }"""),
            "selection_counter": await page.evaluate("""() => {
                const el = document.body.innerText;
                return el.includes('已选择') || el.includes('个文件');
            }"""),
            "errors": [e for e in errors if "favicon" not in e.lower()],
        }
        errors.clear()
        results.append(r2)

        # ── 3. AnonCreator ──
        await page.goto(f"{BASE}/anon/create", timeout=15000, wait_until="networkidle")
        await page.wait_for_timeout(2000)
        r3 = {
            "page": "AnonCreator (/anon/create)",
            "status": "ok",
            "body_text_sample": await page.evaluate("""() =>
                document.body.innerText.substring(0, 500)
            """),
            "has_timeline_btn": await page.evaluate("""() =>
                document.body.innerText.includes('时间线')
            """),
            "has_tree_btn": await page.evaluate("""() =>
                document.body.innerText.includes('已注册')
            """),
            "has_rawfs_btn": await page.evaluate("""() =>
                document.body.innerText.includes('本机目录')
            """),
            "has_commit_btn": await page.evaluate("""() =>
                document.body.innerText.includes('Commit') || document.body.innerText.includes('提交')
            """),
            "has_save_btn": await page.evaluate("""() =>
                document.body.innerText.includes('保存')
            """),
            "file_count_text": await page.evaluate("""() => {
                const t = document.body.innerText;
                const m = t.match(/(\\d+)\\s*(个文件|项)/);
                return m ? m[0] : 'NONE';
            }"""),
            "left_panel_width": await page.evaluate("""() => {
                const panels = document.querySelectorAll('[style*=\"%\"]');
                return panels.length > 0 ? panels[0].style.width : 'NONE';
            }"""),
            "errors": [e for e in errors if "favicon" not in e.lower()],
        }
        errors.clear()
        results.append(r3)

        # ── 4. AnonExplorer (空页面状态) ──
        await page.goto(f"{BASE}/anon", timeout=10000, wait_until="networkidle")
        await page.wait_for_timeout(1000)
        r4 = {
            "page": "AnonExplorer (/anon)",
            "status": "ok",
            "body_text_sample": await page.evaluate("""() =>
                document.body.innerText.substring(0, 300)
            """),
            "errors": [e for e in errors if "favicon" not in e.lower()],
        }
        errors.clear()
        results.append(r4)

        # ── 5. Settings ──
        await page.goto(f"{BASE}/settings", timeout=10000, wait_until="networkidle")
        await page.wait_for_timeout(1000)
        r5 = {
            "page": "Settings (/settings)",
            "status": "ok",
            "has_llm_section": await page.evaluate("""() =>
                document.body.innerText.includes('LLM') || document.body.innerText.includes('模型')
            """),
            "errors": [e for e in errors if "favicon" not in e.lower()],
        }
        errors.clear()
        results.append(r5)

        # ── API check ──
        api_page = await ctx.new_page()
        api_checks = {}
        for name, path in [("ping", "/ping"), ("files", "/files"), ("p2p_status", "/p2p/status"),
                           ("anon_collections", "/anon/collections"), ("collections_public", "/collections/public")]:
            try:
                resp = await api_page.goto(f"{API}{path}", timeout=5000)
                body = await resp.text()
                api_checks[name] = f"HTTP {resp.status}, {len(body)} bytes"
            except Exception as e:
                api_checks[name] = f"ERR: {e}"
        await api_page.close()

        await browser.close()

    # ── Summary ──
    print(json.dumps({"pages": results, "api": api_checks}, indent=2, ensure_ascii=False))

    # Highlight issues
    issues = []
    for r in results:
        if r.get("errors"):
            issues.append(f"⚠ {r['page']}: {len(r['errors'])} console errors")
        if r.get("body_overflow", {}).get("overflowing"):
            issues.append(f"⚠ {r['page']}: body overflow (scrollHeight > clientHeight)")

    if not issues:
        print("\n✅ No critical issues detected")
    else:
        print("\n" + "\n".join(issues))

if __name__ == "__main__":
    asyncio.run(main())
