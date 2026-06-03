-- Refresh tokens com rotação estrita. JWT continua sendo o access
-- token (stateless, TTL curto); refresh é opaque random armazenado
-- hashed pra que vazamento de DB não exponha tokens utilizáveis.
--
-- Fluxo de rotação:
--   1. Cliente manda refresh_token no POST /refresh
--   2. Backend acha pelo hash; se já revogado E replaced_by_id != NULL,
--      detecta REUSO (token roubado tentando ser usado de novo).
--      Reação: revoga TUDO do user (zera todas as sessões).
--   3. Caso normal: marca revoked_at=NOW e replaced_by_id=novo_id;
--      INSERT novo refresh; retorna par novo (access+refresh).
--
-- replaced_by_id é uma "chain" — permite rastrear a cadeia de rotações
-- e detectar reuso preciso. FK SET NULL pra que limpar tokens antigos
-- não cascateie. ON DELETE CASCADE em user_id pra limpeza automática.
--
-- Ordem de INSERT importante na rotação: o novo refresh precisa
-- existir ANTES do UPDATE do antigo (que aponta pra ele via FK).

CREATE TABLE IF NOT EXISTS refresh_tokens (
    id              BIGSERIAL PRIMARY KEY,
    user_id         BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    token_hash      TEXT NOT NULL UNIQUE,
    expires_at      TIMESTAMPTZ NOT NULL,
    revoked_at      TIMESTAMPTZ,
    replaced_by_id  BIGINT REFERENCES refresh_tokens(id) ON DELETE SET NULL,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_rt_user ON refresh_tokens(user_id);
-- Index parcial: tokens ATIVOS (não revogados) — usado pelo logout
-- global ("revoga tudo do user") e pela detecção de reuso.
CREATE INDEX IF NOT EXISTS idx_rt_active
    ON refresh_tokens(user_id) WHERE revoked_at IS NULL;
