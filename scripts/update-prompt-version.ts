#!/usr/bin/env bun
/**
 * update-prompt-version.ts — Creates a new version for an existing prompt template.
 *
 * Fetches the template by name, then POSTs a new version via PUT /admin/prompts/:id.
 *
 * Usage:
 *   AI_MS_URL=http://localhost:8080 GATEWAY_SECRET=xxx \
 *     bun run scripts/update-prompt-version.ts
 */

const AI_MS_URL = process.env.AI_MS_URL     ?? "http://localhost:8080";
const SECRET    = process.env.GATEWAY_SECRET ?? "";
const TENANT_ID = process.env.SEED_TENANT_ID ?? "a0b919ab-e327-418e-841d-e2e9689471b3";

if (!SECRET) {
  console.error("❌  GATEWAY_SECRET env var required");
  process.exit(1);
}

const NEW_SYSTEM_PROMPT = `És um especialista em compatibilidade de hardware de PC. Analisa a lista de componentes fornecida (JSON array) e verifica:
1. Compatibilidade de socket CPU/Motherboard
2. Suporte de RAM (DDR4/DDR5, velocidade máxima)
3. Adequação da PSU (wattagem total + 20% margem)
4. Encaixe físico (GPU comprimento, cooling altura)
5. Conectividade (PCIe slots, M.2, SATA)

Responde APENAS com JSON válido (sem markdown, sem texto extra):
{"findings":[{"severity":"error|warning|info","message":"descrição em pt-PT","components":["tipo1","tipo2"]}]}

Se não houver problemas, responde: {"findings":[]}`;

// 1. List prompts to find the ID of compatibility-check-v1
const listRes = await fetch(`${AI_MS_URL}/admin/prompts?tenant_id=${TENANT_ID}`, {
  headers: { "X-Gateway-Secret": SECRET },
});

if (!listRes.ok) {
  console.error(`❌  Failed to list prompts: HTTP ${listRes.status}`);
  process.exit(1);
}

const { templates } = await listRes.json() as { templates: Array<{ id: string; name: string }> };
const tmpl = templates.find((t) => t.name === "compatibility-check-v1");

if (!tmpl) {
  console.error("❌  Template 'compatibility-check-v1' not found — run seed-prompts.ts first");
  process.exit(1);
}

console.log(`Found template id=${tmpl.id}`);

// 2. Create new version
const updateRes = await fetch(`${AI_MS_URL}/admin/prompts/${tmpl.id}`, {
  method: "PUT",
  headers: {
    "Content-Type":     "application/json",
    "X-Gateway-Secret": SECRET,
  },
  body: JSON.stringify({
    tenant_id:     TENANT_ID,
    name:          "compatibility-check-v1",
    system_prompt: NEW_SYSTEM_PROMPT,
    provider:      "copilot",
    model:         "gpt-4o",
    temperature:   0.1,
    max_tokens:    512,
    cache_ttl_seconds: 7200,
  }),
});

if (!updateRes.ok) {
  const text = await updateRes.text();
  console.error(`❌  Failed to update prompt: HTTP ${updateRes.status} — ${text}`);
  process.exit(1);
}

const version = await updateRes.json() as { version: number };
console.log(`✅  compatibility-check-v1 updated to version ${version.version}`);
