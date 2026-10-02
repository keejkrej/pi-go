// Package ai is the Go port of these pi v1.0.0 TypeScript files
// (authoritative mapping: porting/filemap.tsv):
//
//   - packages/ai/src/api/lazy.ts -> api_lazy.go
//   - packages/ai/src/auth/context.ts -> auth_context.go
//   - packages/ai/src/auth/credential-store.ts -> auth_credential_store.go
//   - packages/ai/src/auth/helpers.ts -> auth_helpers.go
//   - packages/ai/src/auth/resolve.ts -> auth_resolve.go
//   - packages/ai/src/auth/types.ts -> auth_types.go
//   - packages/ai/src/env-api-keys.ts -> env_api_keys.go
//   - packages/ai/src/images-api-registry.ts -> images_api_registry.go
//   - packages/ai/src/model-catalog.ts -> model_catalog.go
//   - packages/ai/src/models-store.ts -> models_store.go
//   - packages/ai/src/models.ts -> models.go
//   - packages/ai/src/session-resources.ts -> session_resources.go
//   - packages/ai/src/types.ts -> types.go
//   - packages/ai/src/utils/abort-signals.ts -> utils_abort_signals.go
//   - packages/ai/src/utils/abort.ts -> utils_abort.go
//   - packages/ai/src/utils/assistant-message-frame.ts -> utils_assistant_message_frame.go
//   - packages/ai/src/utils/diagnostics.ts -> utils_diagnostics.go
//   - packages/ai/src/utils/error-body.ts -> utils_error_body.go
//   - packages/ai/src/utils/estimate.ts -> utils_estimate.go
//   - packages/ai/src/utils/event-stream.ts -> utils_event_stream.go
//   - packages/ai/src/utils/hash.ts -> utils_hash.go
//   - packages/ai/src/utils/headers.ts -> utils_headers.go
//   - packages/ai/src/utils/json-parse.ts -> utils_json_parse.go
//   - packages/ai/src/utils/model-operations.ts -> utils_model_operations.go
//   - packages/ai/src/utils/models-error.ts -> utils_models_error.go
//   - packages/ai/src/utils/node-http-proxy.ts -> utils_node_http_proxy.go
//   - packages/ai/src/utils/oauth-page.ts -> utils_oauth_page.go
//   - packages/ai/src/utils/overflow.ts -> utils_overflow.go
//   - packages/ai/src/utils/pi-user-agent.ts -> utils_pi_user_agent.go
//   - packages/ai/src/utils/provider-env.ts -> utils_provider_env.go
//   - packages/ai/src/utils/provider-retry.ts -> utils_provider_retry.go
//   - packages/ai/src/utils/retry.ts -> utils_retry.go
//   - packages/ai/src/utils/sanitize-unicode.ts -> utils_sanitize_unicode.go
//   - packages/ai/src/utils/sleep.ts -> utils_sleep.go
//   - packages/ai/src/utils/text.ts -> utils_text.go
//   - packages/ai/src/utils/transcript.ts -> utils_transcript.go
//   - packages/ai/src/utils/typebox-helpers.ts -> utils_typebox_helpers.go
//   - packages/ai/src/utils/uuid.ts -> utils_uuid.go
//   - packages/ai/src/utils/validation.ts -> utils_validation.go
package ai
