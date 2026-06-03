-- Verificação de email no register + reset de senha por email.
--
-- Decisões:
--   - Token armazenado SOMENTE como SHA-256 hex. Vazamento do DB
--     não expõe tokens utilizáveis. Lookup é por hash (constant-time
--     compare é desnecessário porque a comparação é contra hash de
--     32 bytes que o caller deriva no lookup, não contra o token raw).
--   - `used_at` em vez de DELETE pra deixar rastro de auditoria
--     (também ajuda a detectar reuso suspeito).
--   - Expiração no schema (`expires_at`) + checada no app — verificação
--     de "vencido" é só comparar timestamps, sem precisar de cron.
--   - CASCADE no user_id: usuário deletado limpa tokens órfãos.
--
-- `users.email_verified_at` é NULL até o user clicar no link de
-- confirmação. Backend bloqueia POST /assets quando NULL (UX gate),
-- mas login/browse continuam liberados pra que o user consiga ler o
-- email e completar o fluxo.

DO $$
BEGIN
  IF NOT EXISTS (
    SELECT 1 FROM information_schema.columns
     WHERE table_name = 'users' AND column_name = 'email_verified_at'
  ) THEN
    ALTER TABLE users ADD COLUMN email_verified_at TIMESTAMPTZ;
  END IF;
END$$;

CREATE TABLE IF NOT EXISTS email_verification_tokens (
    id          BIGSERIAL PRIMARY KEY,
    user_id     BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    token_hash  TEXT NOT NULL UNIQUE,
    expires_at  TIMESTAMPTZ NOT NULL,
    used_at     TIMESTAMPTZ,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_evt_user ON email_verification_tokens(user_id);

CREATE TABLE IF NOT EXISTS password_reset_tokens (
    id          BIGSERIAL PRIMARY KEY,
    user_id     BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    token_hash  TEXT NOT NULL UNIQUE,
    expires_at  TIMESTAMPTZ NOT NULL,
    used_at     TIMESTAMPTZ,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_prt_user ON password_reset_tokens(user_id);
