// Package catalog is the Go port of these pi v1.0.0 TypeScript files
// (authoritative mapping: porting/filemap.tsv):
//
//   - packages/ai/src/models.generated.ts -> models_generated.go
//   - packages/ai/src/providers/amazon-bedrock.models.ts -> amazon_bedrock_models.go
//   - packages/ai/src/providers/ant-ling.models.ts -> ant_ling_models.go
//   - packages/ai/src/providers/anthropic.models.ts -> anthropic_models.go
//   - packages/ai/src/providers/azure-openai-responses.models.ts -> azure_openai_responses_models.go
//   - packages/ai/src/providers/baseten.models.ts -> baseten_models.go
//   - packages/ai/src/providers/cerebras.models.ts -> cerebras_models.go
//   - packages/ai/src/providers/cloudflare-ai-gateway.models.ts -> cloudflare_ai_gateway_models.go
//   - packages/ai/src/providers/cloudflare-workers-ai.models.ts -> cloudflare_workers_ai_models.go
//   - packages/ai/src/providers/deepseek.models.ts -> deepseek_models.go
//   - packages/ai/src/providers/fireworks.models.ts -> fireworks_models.go
//   - packages/ai/src/providers/github-copilot.models.ts -> github_copilot_models.go
//   - packages/ai/src/providers/google-vertex.models.ts -> google_vertex_models.go
//   - packages/ai/src/providers/google.models.ts -> google_models.go
//   - packages/ai/src/providers/groq.models.ts -> groq_models.go
//   - packages/ai/src/providers/huggingface.models.ts -> huggingface_models.go
//   - packages/ai/src/providers/kimi-coding.models.ts -> kimi_coding_models.go
//   - packages/ai/src/providers/meta.models.ts -> meta_models.go
//   - packages/ai/src/providers/minimax-cn.models.ts -> minimax_cn_models.go
//   - packages/ai/src/providers/minimax.models.ts -> minimax_models.go
//   - packages/ai/src/providers/mistral.models.ts -> mistral_models.go
//   - packages/ai/src/providers/moonshotai-cn.models.ts -> moonshotai_cn_models.go
//   - packages/ai/src/providers/moonshotai.models.ts -> moonshotai_models.go
//   - packages/ai/src/providers/nvidia.models.ts -> nvidia_models.go
//   - packages/ai/src/providers/openai-codex.models.ts -> openai_codex_models.go
//   - packages/ai/src/providers/openai.models.ts -> openai_models.go
//   - packages/ai/src/providers/opencode-go.models.ts -> opencode_go_models.go
//   - packages/ai/src/providers/opencode.models.ts -> opencode_models.go
//   - packages/ai/src/providers/openrouter.models.ts -> openrouter_models.go
//   - packages/ai/src/providers/qwen-token-plan-cn.models.ts -> qwen_token_plan_cn_models.go
//   - packages/ai/src/providers/qwen-token-plan-individual.models.ts -> qwen_token_plan_individual_models.go
//   - packages/ai/src/providers/qwen-token-plan.models.ts -> qwen_token_plan_models.go
//   - packages/ai/src/providers/radius.models.ts -> radius_models.go
//   - packages/ai/src/providers/together.models.ts -> together_models.go
//   - packages/ai/src/providers/typesafe.models.ts -> typesafe_models.go
//   - packages/ai/src/providers/vercel-ai-gateway.models.ts -> vercel_ai_gateway_models.go
//   - packages/ai/src/providers/xai.models.ts -> xai_models.go
//   - packages/ai/src/providers/xiaomi-token-plan-ams.models.ts -> xiaomi_token_plan_ams_models.go
//   - packages/ai/src/providers/xiaomi-token-plan-cn.models.ts -> xiaomi_token_plan_cn_models.go
//   - packages/ai/src/providers/xiaomi-token-plan-sgp.models.ts -> xiaomi_token_plan_sgp_models.go
//   - packages/ai/src/providers/xiaomi.models.ts -> xiaomi_models.go
//   - packages/ai/src/providers/zai-coding-cn.models.ts -> zai_coding_cn_models.go
//   - packages/ai/src/providers/zai.models.ts -> zai_models.go
package catalog
