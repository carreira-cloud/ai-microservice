#!/usr/bin/env bun
/**
 * seed-prompts.ts — Phase 9.0: Seed initial prompt templates in ai-microservice.
 *
 * Idempotent: ignores 409 Conflict (template already exists).
 * Run BEFORE deploying eurosys-site refactor.
 *
 * Usage:
 *   AI_MS_URL=http://localhost:8080 GATEWAY_SECRET=xxx bun run scripts/seed-prompts.ts
 *   AI_MS_URL=https://api.tst.carreira.cloud/ai GATEWAY_SECRET=xxx bun run scripts/seed-prompts.ts
 */

const AI_MS_URL  = process.env.AI_MS_URL     ?? "http://localhost:8080";
const SECRET     = process.env.GATEWAY_SECRET ?? "";
const TENANT_ID  = process.env.SEED_TENANT_ID ?? "a0b919ab-e327-418e-841d-e2e9689471b3"; // Eurosys canonical UUID

if (!SECRET) {
  console.error("❌  GATEWAY_SECRET env var required");
  process.exit(1);
}

interface PromptSeed {
  name:          string;
  description:   string;
  system_prompt: string;
  provider?:     string;
  model?:        string;
  temperature?:  number;
  max_tokens?:   number;
  cache_ttl_seconds?: number;
}

const PROMPTS: PromptSeed[] = [
  {
    name:        "recommend-spec-v1",
    description: "Gera uma especificação de PC com base nas respostas do questionário do Builder Assistant.",
    system_prompt: `És um especialista em hardware de computadores. Analisa as respostas do questionário do utilizador e gera uma especificação detalhada de PC.

Responde SEMPRE em JSON com esta estrutura:
{
  "name": "string",
  "description": "string",
  "use_case": "string",
  "components": {
    "cpu": "string",
    "gpu": "string",
    "ram": "string",
    "storage": "string",
    "motherboard": "string",
    "psu": "string",
    "cooling": "string",
    "case": "string"
  },
  "estimated_price_eur": number,
  "performance_tier": "entry|mid|high|ultra"
}

Sê concreto com modelos e capacidades. Prioriza a relação qualidade-preço.`,
    provider:    "copilot",
    model:       "gpt-4o",
    temperature: 0.3,
    max_tokens:  1024,
    cache_ttl_seconds: 3600,
  },
  {
    name:        "compatibility-check-v1",
    description: "Verifica a compatibilidade de componentes de PC e identifica conflitos.",
    system_prompt: `És um especialista em compatibilidade de hardware de PC. Analisa a lista de componentes fornecida (JSON array) e verifica:
1. Compatibilidade de socket CPU/Motherboard
2. Suporte de RAM (DDR4/DDR5, velocidade máxima)
3. Adequação da PSU (wattagem total + 20% margem)
4. Encaixe físico (GPU comprimento, cooling altura)
5. Conectividade (PCIe slots, M.2, SATA)

Responde APENAS com JSON válido (sem markdown, sem texto extra):
{"findings":[{"severity":"error|warning|info","message":"descrição em pt-PT","components":["tipo1","tipo2"]}]}

Se não houver problemas, responde: {"findings":[]}`,
    provider:    "copilot",
    model:       "gpt-4o",
    temperature: 0.1,
    max_tokens:  512,
    cache_ttl_seconds: 7200,
  },
  {
    name:        "compound-recommendations-v1",
    description: "Gera recomendações de builds Niposom com base no perfil e orçamento do utilizador.",
    system_prompt: `És um assistente de vendas especializado em PCs pré-configurados (builds Niposom).
Dado o perfil do utilizador (uso, orçamento, preferências), recomenda 3 builds ordenadas por relevância.

Responde em JSON:
{
  "recommendations": [
    {
      "rank": number,
      "compound_id": "string",
      "name": "string",
      "rationale": "string (1 frase)",
      "fit_score": number (0-100)
    }
  ],
  "summary": "string (1-2 frases para o utilizador)"
}

Sê directo e concreto. Não inventes IDs — usa apenas os compound_id fornecidos no contexto.`,
    provider:    "copilot",
    model:       "gpt-4o",
    temperature: 0.4,
    max_tokens:  768,
    cache_ttl_seconds: 1800,
  },
];

async function seedPrompt(p: PromptSeed): Promise<void> {
  const url     = `${AI_MS_URL}/admin/prompts`;
  const payload = { tenant_id: TENANT_ID, ...p };

  const res = await fetch(url, {
    method:  "POST",
    headers: {
      "Content-Type":    "application/json",
      "X-Gateway-Secret": SECRET,
    },
    body: JSON.stringify(payload),
  });

  if (res.status === 201) {
    const body = await res.json() as { id: string };
    console.log(`✅  ${p.name} created (id=${body.id})`);
  } else if (res.status === 409) {
    console.log(`⏭️   ${p.name} already exists — skipping`);
  } else {
    const text = await res.text();
    throw new Error(`${p.name}: HTTP ${res.status} — ${text}`);
  }
}

console.log(`🌱  Seeding ${PROMPTS.length} prompts → ${AI_MS_URL} (tenant=${TENANT_ID})`);
for (const p of PROMPTS) {
  await seedPrompt(p);
}
console.log("✅  Seed complete");
