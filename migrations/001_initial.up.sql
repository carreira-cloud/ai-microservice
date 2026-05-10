-- 001_initial.up.sql — initial schema for ai-microservice

CREATE TABLE IF NOT EXISTS prompt_templates (
    id               VARCHAR(36)  PRIMARY KEY,
    name             TEXT         NOT NULL,
    description      TEXT,
    active_version_id VARCHAR(36),
    tenant_id        VARCHAR(64)  NOT NULL,
    created_at       TIMESTAMPTZ  NOT NULL DEFAULT NOW(),
    updated_at       TIMESTAMPTZ  NOT NULL DEFAULT NOW(),
    UNIQUE (tenant_id, name)
);
CREATE INDEX IF NOT EXISTS idx_prompt_templates_tenant ON prompt_templates (tenant_id);

CREATE TABLE IF NOT EXISTS prompt_versions (
    id            VARCHAR(36) PRIMARY KEY,
    template_id   VARCHAR(36) NOT NULL REFERENCES prompt_templates(id) ON DELETE CASCADE,
    system_prompt TEXT        NOT NULL,
    provider      VARCHAR(32) NOT NULL DEFAULT 'copilot',
    model         VARCHAR(64) NOT NULL DEFAULT 'gpt-4o',
    temperature   FLOAT       NOT NULL DEFAULT 0.3,
    max_tokens    INT         NOT NULL DEFAULT 1024,
    cache_ttl_sec INT         NOT NULL DEFAULT 3600,
    version       INT         NOT NULL,
    created_at    TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX IF NOT EXISTS idx_prompt_versions_template ON prompt_versions (template_id);

CREATE TABLE IF NOT EXISTS ai_audit_logs (
    id            VARCHAR(36)  PRIMARY KEY,
    tenant_id     VARCHAR(64)  NOT NULL,
    template_id   VARCHAR(36),
    provider      VARCHAR(32),
    model         VARCHAR(64),
    prompt_hash   VARCHAR(64),
    response_hash VARCHAR(64),
    latency_ms    BIGINT,
    cached        BOOLEAN      NOT NULL DEFAULT FALSE,
    idempotent    BOOLEAN      NOT NULL DEFAULT FALSE,
    error         TEXT,
    created_at    TIMESTAMPTZ  NOT NULL DEFAULT NOW()
);
CREATE INDEX IF NOT EXISTS idx_audit_tenant_time ON ai_audit_logs (tenant_id, created_at);
CREATE INDEX IF NOT EXISTS idx_audit_template    ON ai_audit_logs (template_id);
