const UNIT_MS: Record<string, number> = {
	ms: 1,
	millisecond: 1,
	milliseconds: 1,
	s: 1000,
	sec: 1000,
	secs: 1000,
	second: 1000,
	seconds: 1000,
	m: 60 * 1000,
	min: 60 * 1000,
	mins: 60 * 1000,
	minute: 60 * 1000,
	minutes: 60 * 1000,
	h: 60 * 60 * 1000,
	hr: 60 * 60 * 1000,
	hrs: 60 * 60 * 1000,
	hour: 60 * 60 * 1000,
	hours: 60 * 60 * 1000,
	d: 24 * 60 * 60 * 1000,
	day: 24 * 60 * 60 * 1000,
	days: 24 * 60 * 60 * 1000,
};

export function parseDurationMs(input: string): number | undefined {
	const match = /^\s*(-?(?:\d+)?\.?\d+)\s*([a-zA-Z]*)\s*$/.exec(String(input ?? ""));
	if (!match) return undefined;
	const value = Number.parseFloat(match[1]);
	if (!Number.isFinite(value)) return undefined;
	const unit = (match[2] || "ms").toLowerCase();
	const factor = UNIT_MS[unit];
	if (!factor) return undefined;
	return value * factor;
}
