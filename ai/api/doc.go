// Package api is the Go port of these pi v1.0.0 TypeScript files
// (authoritative mapping: porting/filemap.tsv):
//
//   - packages/ai/src/api/anthropic-messages.lazy.ts -> anthropic_messages_lazy.go
//   - packages/ai/src/api/anthropic-messages.ts -> anthropic_messages.go
//   - packages/ai/src/api/azure-openai-responses.lazy.ts -> azure_openai_responses_lazy.go
//   - packages/ai/src/api/azure-openai-responses.ts -> azure_openai_responses.go
//   - packages/ai/src/api/bedrock-converse-stream.lazy.ts -> bedrock_converse_stream_lazy.go
//   - packages/ai/src/api/bedrock-converse-stream.ts -> bedrock_converse_stream.go
//   - packages/ai/src/api/cloudflare-ai-binding.ts -> cloudflare_ai_binding.go
//   - packages/ai/src/api/cloudflare-workers-ai-system-one.lazy.ts -> cloudflare_workers_ai_system_one_lazy.go
//   - packages/ai/src/api/cloudflare-workers-ai-system-one.ts -> cloudflare_workers_ai_system_one.go
//   - packages/ai/src/api/cloudflare.ts -> cloudflare.go
//   - packages/ai/src/api/constrained-sampling.ts -> constrained_sampling.go
//   - packages/ai/src/api/github-copilot-headers.ts -> github_copilot_headers.go
//   - packages/ai/src/api/google-generative-ai.lazy.ts -> google_generative_ai_lazy.go
//   - packages/ai/src/api/google-generative-ai.ts -> google_generative_ai.go
//   - packages/ai/src/api/google-shared.ts -> google_shared.go
//   - packages/ai/src/api/google-vertex.lazy.ts -> google_vertex_lazy.go
//   - packages/ai/src/api/google-vertex.ts -> google_vertex.go
//   - packages/ai/src/api/llama-cpp-classify.lazy.ts -> llama_cpp_classify_lazy.go
//   - packages/ai/src/api/llama-cpp-classify.ts -> llama_cpp_classify.go
//   - packages/ai/src/api/mistral-conversations.lazy.ts -> mistral_conversations_lazy.go
//   - packages/ai/src/api/mistral-conversations.ts -> mistral_conversations.go
//   - packages/ai/src/api/openai-codex-responses.lazy.ts -> openai_codex_responses_lazy.go
//   - packages/ai/src/api/openai-codex-responses.ts -> openai_codex_responses.go
//   - packages/ai/src/api/openai-completions.lazy.ts -> openai_completions_lazy.go
//   - packages/ai/src/api/openai-completions.ts -> openai_completions.go
//   - packages/ai/src/api/openai-prompt-cache.ts -> openai_prompt_cache.go
//   - packages/ai/src/api/openai-responses-shared.ts -> openai_responses_shared.go
//   - packages/ai/src/api/openai-responses.lazy.ts -> openai_responses_lazy.go
//   - packages/ai/src/api/openai-responses.ts -> openai_responses.go
//   - packages/ai/src/api/openrouter-images.lazy.ts -> openrouter_images_lazy.go
//   - packages/ai/src/api/openrouter-images.ts -> openrouter_images.go
//   - packages/ai/src/api/pi-messages.lazy.ts -> pi_messages_lazy.go
//   - packages/ai/src/api/pi-messages.ts -> pi_messages.go
//   - packages/ai/src/api/simple-options.ts -> simple_options.go
//   - packages/ai/src/api/system-one-shared.ts -> system_one_shared.go
//   - packages/ai/src/api/transform-messages.ts -> transform_messages.go
//   - packages/ai/src/api/typesafe-system-one.lazy.ts -> typesafe_system_one_lazy.go
//   - packages/ai/src/api/typesafe-system-one.ts -> typesafe_system_one.go
package api
