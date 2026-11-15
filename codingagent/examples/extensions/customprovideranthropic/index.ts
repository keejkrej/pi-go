/**
 * Custom Anthropic Provider Example (QuickJS-compatible via fetch + WebCrypto).
 */

import {
	type AssistantMessage,
	type AssistantMessageEventStream,
	calculateCost,
	collapseSystemMessages,
	createAssistantMessageEventStream,
	getCurrentSystemPrompt,
	getCurrentTools,
	type Model,
	type OAuthCredentials,
	type OAuthLoginCallbacks,
	type SimpleStreamOptions,
	type StopReason,
	type TranscriptContext,
} from "@earendil-works/pi-ai";
import type { ExtensionAPI } from "@earendil-works/pi-coding-agent";

const decode = (s: string) => atob(s);
const CLIENT_ID = decode("OWQxYzI1MGEtZTYxYi00NGQ5LTg4ZWQtNTk0NGQxOTYyZjVl");
const AUTHORIZE_URL = "https://claude.ai/oauth/authorize";
const TOKEN_URL = "https://console.anthropic.com/v1/oauth/token";
const REDIRECT_URI = "https://console.anthropic.com/oauth/code/callback";
const SCOPES = "org:create_api_key user:profile user:inference";

async function generatePKCE(): Promise<{ verifier: string; challenge: string }> {
	const array = new Uint8Array(32);
	crypto.getRandomValues(array);
	const verifier = btoa(String.fromCharCode(...array))
		.replace(/\+/g, "-")
		.replace(/\//g, "_")
		.replace(/=+$/, "");

	const data = new TextEncoder().encode(verifier);
	const hash = await crypto.subtle.digest("SHA-256", data);
	const challenge = btoa(String.fromCharCode(...new Uint8Array(hash)))
		.replace(/\+/g, "-")
		.replace(/\//g, "_")
		.replace(/=+$/, "");

	return { verifier, challenge };
}

async function loginAnthropic(callbacks: OAuthLoginCallbacks): Promise<OAuthCredentials> {
	const { verifier, challenge } = await generatePKCE();
	const authParams = new URLSearchParams({
		code: "true",
		client_id: CLIENT_ID,
		response_type: "code",
		redirect_uri: REDIRECT_URI,
		scope: SCOPES,
		code_challenge: challenge,
		code_challenge_method: "S256",
		state: verifier,
	});
	callbacks.onAuth({ url: `${AUTHORIZE_URL}?${authParams.toString()}` });
	const authCode = await callbacks.onPrompt({ message: "Paste the authorization code:" });
	const [code, state] = authCode.split("#");
	const resp = await fetch(TOKEN_URL, {
		method: "POST",
		headers: { "Content-Type": "application/json" },
		body: JSON.stringify({
			grant_type: "authorization_code",
			client_id: CLIENT_ID,
			code,
			state,
			redirect_uri: REDIRECT_URI,
			code_verifier: verifier,
		}),
	});
	if (!resp.ok) {
		throw new Error(`Token exchange failed: ${resp.status}`);
	}
	const tokenData = await resp.json();
	return {
		refresh: tokenData.refresh_token,
		access: tokenData.access_token,
		expires: Date.now() + Number(tokenData.expires_in || 3600) * 1000 - 5 * 60 * 1000,
	};
}

async function refreshAnthropicToken(credentials: OAuthCredentials): Promise<OAuthCredentials> {
	const resp = await fetch(TOKEN_URL, {
		method: "POST",
		headers: { "Content-Type": "application/json" },
		body: JSON.stringify({
			grant_type: "refresh_token",
			client_id: CLIENT_ID,
			refresh_token: credentials.refresh,
		}),
	});
	if (!resp.ok) {
		throw new Error(`Token refresh failed: ${resp.status}`);
	}
	const data = await resp.json();
	return {
		refresh: data.refresh_token,
		access: data.access_token,
		expires: Date.now() + Number(data.expires_in || 3600) * 1000 - 5 * 60 * 1000,
	};
}

function mapStopReason(reason: string | null | undefined): StopReason {
	switch (reason) {
		case "end_turn":
		case "pause_turn":
		case "stop_sequence":
			return "stop";
		case "max_tokens":
			return "length";
		case "tool_use":
			return "toolUse";
		default:
			return "error";
	}
}

function convertMessages(transcript: TranscriptContext) {
	const messages: Array<{ role: "user" | "assistant"; content: unknown }> = [];
	const source = transcript?.messages || [];
	for (const msg of source) {
		if (!msg || typeof msg !== "object") continue;
		if (msg.role === "user") {
			messages.push({
				role: "user",
				content: typeof msg.content === "string" ? msg.content : msg.content || "",
			});
		} else if (msg.role === "assistant") {
			messages.push({ role: "assistant", content: msg.content || [] });
		}
	}
	return messages;
}

function parseSSEEvents(bodyText: string): Array<Record<string, any>> {
	const events: Array<Record<string, any>> = [];
	for (const line of bodyText.split(/\r?\n/)) {
		if (!line.startsWith("data:")) continue;
		const payload = line.slice(5).trim();
		if (!payload || payload === "[DONE]") continue;
		try {
			events.push(JSON.parse(payload));
		} catch {
			// Skip non-JSON SSE keepalives
		}
	}
	return events;
}

function streamCustomAnthropic(
	model: Model<any>,
	transcript: TranscriptContext,
	options?: SimpleStreamOptions,
): AssistantMessageEventStream {
	const stream = createAssistantMessageEventStream();

	(async () => {
		const output: AssistantMessage = {
			role: "assistant",
			content: [],
			api: model.api,
			provider: model.provider,
			model: model.id,
			usage: {
				input: 0,
				output: 0,
				cacheRead: 0,
				cacheWrite: 0,
				totalTokens: 0,
				cost: { input: 0, output: 0, cacheRead: 0, cacheWrite: 0, total: 0 },
			},
			stopReason: "stop",
			timestamp: Date.now(),
		};

		try {
			const apiKey = options?.apiKey || process.env.CUSTOM_ANTHROPIC_API_KEY || "";
			const baseUrl = (model.baseUrl || "https://api.anthropic.com").replace(/\/$/, "");
			const collapsed = collapseSystemMessages(transcript || { messages: [] });
			const systemPrompt = getCurrentSystemPrompt(collapsed.messages || []);
			const tools = getCurrentTools(collapsed.messages || []).map((t: any) => ({
				name: t.name,
				description: t.description,
				input_schema: t.parameters || { type: "object", properties: {} },
			}));

			const reqBody: Record<string, any> = {
				model: model.id,
				max_tokens: options?.maxTokens || model.maxTokens || 4096,
				stream: true,
				messages: convertMessages(collapsed),
			};
			if (systemPrompt) reqBody.system = systemPrompt;
			if (tools.length > 0) reqBody.tools = tools;

			const headers: Record<string, string> = {
				"Content-Type": "application/json",
				"anthropic-version": "2023-06-01",
				...(options?.headers || {}),
			};
			if (apiKey) headers["x-api-key"] = apiKey;

			const resp = await fetch(`${baseUrl}/v1/messages`, {
				method: "POST",
				headers,
				body: JSON.stringify(reqBody),
				signal: options?.signal,
			});
			if (!resp.ok) {
				const errText = await resp.text();
				throw new Error(`Anthropic HTTP ${resp.status}: ${errText}`);
			}

			const sseText = await resp.text();
			const blocks = output.content as any[];
			const blockIndices = new Map<number, number>();
			stream.push({ type: "start", partial: output });

			for (const ev of parseSSEEvents(sseText)) {
				if (ev.type === "message_start" && ev.message?.usage) {
					output.usage.input = ev.message.usage.input_tokens || 0;
					output.usage.output = ev.message.usage.output_tokens || 0;
				} else if (ev.type === "content_block_start") {
					const idx = blocks.length;
					blockIndices.set(ev.index, idx);
					if (ev.content_block?.type === "text") {
						blocks.push({ type: "text", text: ev.content_block.text || "" });
						stream.push({ type: "text_start", contentIndex: idx, partial: output });
					} else if (ev.content_block?.type === "tool_use") {
						blocks.push({
							type: "toolCall",
							id: ev.content_block.id || "",
							name: ev.content_block.name || "",
							arguments: ev.content_block.input || {},
							_partialJson: "",
						});
						stream.push({ type: "toolcall_start", contentIndex: idx, partial: output });
					}
				} else if (ev.type === "content_block_delta") {
					const idx = blockIndices.get(ev.index);
					if (idx === undefined) continue;
					const blk = blocks[idx];
					if (ev.delta?.type === "text_delta" && blk?.type === "text") {
						blk.text += ev.delta.text || "";
						stream.push({ type: "text_delta", contentIndex: idx, delta: ev.delta.text || "", partial: output });
					} else if (ev.delta?.type === "input_json_delta" && blk?.type === "toolCall") {
						blk._partialJson = (blk._partialJson || "") + (ev.delta.partial_json || "");
						try {
							blk.arguments = JSON.parse(blk._partialJson);
						} catch {
							// Partial JSON incomplete until block stop
						}
						stream.push({
							type: "toolcall_delta",
							contentIndex: idx,
							delta: ev.delta.partial_json || "",
							partial: output,
						});
					}
				} else if (ev.type === "content_block_stop") {
					const idx = blockIndices.get(ev.index);
					if (idx === undefined) continue;
					const blk = blocks[idx];
					if (blk?.type === "text") {
						stream.push({ type: "text_end", contentIndex: idx, content: blk.text, partial: output });
					} else if (blk?.type === "toolCall") {
						delete blk._partialJson;
						stream.push({ type: "toolcall_end", contentIndex: idx, toolCall: blk, partial: output });
					}
				} else if (ev.type === "message_delta") {
					if (ev.delta?.stop_reason) {
						output.stopReason = mapStopReason(ev.delta.stop_reason);
					}
					if (ev.usage?.output_tokens !== undefined) {
						output.usage.output = ev.usage.output_tokens;
					}
				}
			}

			output.usage.totalTokens = output.usage.input + output.usage.output;
			calculateCost(model, output.usage);
			stream.push({ type: "done", reason: output.stopReason, message: output });
			stream.end(output);
		} catch (err) {
			output.stopReason = options?.signal?.aborted ? "aborted" : "error";
			output.errorMessage = err instanceof Error ? err.message : String(err);
			stream.push({ type: "error", reason: output.stopReason, error: output });
			stream.end(output);
		}
	})();

	return stream;
}

export default function (pi: ExtensionAPI) {
	pi.registerProvider("custom-anthropic", {
		baseUrl: "https://api.anthropic.com",
		apiKey: "$CUSTOM_ANTHROPIC_API_KEY",
		api: "custom-anthropic-api",
		models: [
			{
				id: "claude-opus-4-5",
				name: "Claude Opus 4.5 (Custom)",
				reasoning: true,
				input: ["text", "image"],
				cost: { input: 5, output: 25, cacheRead: 0.5, cacheWrite: 6.25 },
				contextWindow: 200000,
				maxTokens: 64000,
			},
			{
				id: "claude-sonnet-4-5",
				name: "Claude Sonnet 4.5 (Custom)",
				reasoning: true,
				input: ["text", "image"],
				cost: { input: 3, output: 15, cacheRead: 0.3, cacheWrite: 3.75 },
				contextWindow: 200000,
				maxTokens: 64000,
			},
		],
		oauth: {
			name: "Custom Anthropic (Claude Pro/Max)",
			login: loginAnthropic,
			refreshToken: refreshAnthropicToken,
			getApiKey: (cred: OAuthCredentials) => cred.access,
		},
		streamSimple: streamCustomAnthropic,
	});
}
