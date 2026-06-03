import { useState, type FormEvent } from 'react'
import { Link, useNavigate, useSearchParams } from 'react-router-dom'
import { ApiError, api } from '../api/client'

// /reset?token=... — form de nova senha. Token vem na query string
// (link do email). Em sucesso, navega pro /login com banner de "senha
// atualizada"; em falha (token inválido/expirado), mostra erro inline.

export default function ResetPassword() {
  const [params] = useSearchParams()
  const navigate = useNavigate()
  const token = params.get('token') ?? ''

  const [password, setPassword] = useState('')
  const [confirm, setConfirm] = useState('')
  const [submitting, setSubmitting] = useState(false)
  const [error, setError] = useState<string | null>(null)

  const canSubmit =
    token.length > 0 &&
    password.length >= 8 &&
    password === confirm &&
    !submitting

  async function handleSubmit(e: FormEvent) {
    e.preventDefault()
    if (!canSubmit) return
    setSubmitting(true)
    setError(null)
    try {
      await api.post('/api/v1/reset-password', {
        token,
        new_password: password,
      })
      navigate('/login', {
        replace: true,
        state: { resetSuccess: true },
      })
    } catch (err) {
      setError(messageForReset(err))
      setSubmitting(false)
    }
  }

  if (!token) {
    return (
      <div className="mx-auto max-w-sm mt-12 p-6">
        <div className="bg-ink text-parchment border-4 border-ink shadow-pixel p-8 text-center">
          <p className="text-sm font-bold uppercase tracking-widest mb-6">
            Link sem token
          </p>
          <Link
            to="/forgot"
            className="bg-parchment text-ink border-4 border-ink shadow-pixel px-4 py-2 text-xs font-bold uppercase tracking-widest"
          >
            ▶ Pedir novo link
          </Link>
        </div>
      </div>
    )
  }

  return (
    <div className="mx-auto max-w-sm mt-12 p-6">
      <div className="bg-parchment border-4 border-ink shadow-pixel">
        <h1 className="bg-arcane text-parchment text-sm font-bold uppercase tracking-widest border-b-4 border-ink px-4 py-3">
          ▶ Nova senha
        </h1>
        {error && (
          <div role="alert" className="bg-twilight text-parchment border-b-4 border-ink px-6 py-3 text-xs uppercase tracking-widest">
            ✗ {error}
          </div>
        )}
        <form onSubmit={handleSubmit} className="p-6 space-y-4">
          <label className="block space-y-1">
            <span className="text-xs uppercase tracking-widest font-bold text-ink/70">
              Nova senha (mín. 8 caracteres)
            </span>
            <input
              type="password"
              value={password}
              onChange={(e) => setPassword(e.target.value)}
              minLength={8}
              required
              autoFocus
              className="w-full bg-parchment border-2 border-ink px-3 py-2 text-sm focus:outline-none focus:bg-arcane/10"
            />
          </label>
          <label className="block space-y-1">
            <span className="text-xs uppercase tracking-widest font-bold text-ink/70">
              Confirme
            </span>
            <input
              type="password"
              value={confirm}
              onChange={(e) => setConfirm(e.target.value)}
              minLength={8}
              required
              className="w-full bg-parchment border-2 border-ink px-3 py-2 text-sm focus:outline-none focus:bg-arcane/10"
            />
            {confirm.length > 0 && password !== confirm && (
              <span className="block text-[10px] uppercase tracking-widest text-arcane">
                ▸ Senhas não batem
              </span>
            )}
          </label>
          <button
            type="submit"
            disabled={!canSubmit}
            className="
              w-full bg-arcane text-parchment border-4 border-ink shadow-pixel
              px-4 py-3 text-sm font-bold uppercase tracking-widest
              transition-all duration-75 ease-out
              hover:translate-x-[2px] hover:translate-y-[2px] hover:shadow-none
              disabled:opacity-50 disabled:hover:translate-x-0 disabled:hover:translate-y-0 disabled:hover:shadow-pixel
            "
          >
            {submitting ? '...' : '▶ Redefinir senha'}
          </button>
        </form>
      </div>
    </div>
  )
}

function messageForReset(err: unknown): string {
  if (err instanceof ApiError) {
    if (err.status === 400) return 'Token inválido — peça um novo link'
    if (err.status === 410) return 'Token expirou — peça um novo link'
  }
  return 'Falha ao redefinir senha'
}
