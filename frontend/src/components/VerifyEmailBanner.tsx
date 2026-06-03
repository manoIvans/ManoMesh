import { useState } from 'react'
import { api } from '../api/client'
import { useAuth } from '../auth/AuthContext'
import { useToast } from './Toast'

// VerifyEmailBanner: aparece no topo das páginas autenticadas quando
// o user logado ainda não confirmou o email (currentUser.email_verified_at
// é null). Botão "Reenviar email" chama POST /resend-verification e
// some quando o backend marca o user como verificado (refresh do
// currentUser via página /verify).
//
// Esconde quando: não autenticado, ainda carregando currentUser, ou
// já verificado.

export default function VerifyEmailBanner() {
  const { isAuthenticated, currentUser } = useAuth()
  const toast = useToast()
  const [sending, setSending] = useState(false)
  const [sent, setSent] = useState(false)

  if (!isAuthenticated || !currentUser) return null
  if (currentUser.email_verified_at) return null

  async function handleResend() {
    setSending(true)
    try {
      await api.post('/api/v1/resend-verification')
      setSent(true)
      toast.success('Email enviado — confira sua caixa')
    } catch {
      toast.error('Falha ao reenviar — tente novamente em alguns minutos')
    } finally {
      setSending(false)
    }
  }

  return (
    <div
      role="status"
      className="bg-twilight text-parchment border-b-4 border-ink px-4 py-2 flex flex-wrap items-center justify-between gap-3"
    >
      <p className="text-xs uppercase tracking-widest font-bold">
        ▸ Confirme seu email pra desbloquear publicar assets
      </p>
      <button
        type="button"
        onClick={handleResend}
        disabled={sending || sent}
        className="
          bg-parchment text-ink border-2 border-ink shadow-pixel-sm
          px-3 py-1 text-[10px] font-bold uppercase tracking-widest
          transition-all duration-75 ease-out
          hover:translate-x-[1px] hover:translate-y-[1px] hover:shadow-none
          disabled:opacity-50
        "
      >
        {sent ? '✓ Enviado' : sending ? '...' : '▶ Reenviar email'}
      </button>
    </div>
  )
}
