/**
 * Syncs pi theme with macOS system appearance (dark/light mode).
 *
 * Adapted for QuickJS runtime: uses pi.exec("osascript", ...) instead of node:child_process.
 */

import type { ExtensionAPI } from "@earendil-works/pi-coding-agent";

async function isDarkMode(pi: ExtensionAPI): Promise<boolean> {
	try {
		const res = await pi.exec("osascript", [
			"-e",
			'tell application "System Events" to tell appearance preferences to return dark mode',
		]);
		return res.code === 0 && res.stdout.trim() === "true";
	} catch {
		return false;
	}
}

export default function (pi: ExtensionAPI) {
	let intervalId: ReturnType<typeof setInterval> | null = null;

	pi.on("session_start", async (_event, ctx) => {
		let currentTheme = (await isDarkMode(pi)) ? "dark" : "light";
		ctx.ui.setTheme(currentTheme);

		intervalId = setInterval(async () => {
			const newTheme = (await isDarkMode(pi)) ? "dark" : "light";
			if (newTheme !== currentTheme) {
				currentTheme = newTheme;
				ctx.ui.setTheme(currentTheme);
			}
		}, 2000);
	});

	pi.on("session_shutdown", () => {
		if (intervalId) {
			clearInterval(intervalId);
			intervalId = null;
		}
	});
}
