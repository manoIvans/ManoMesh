import { useState, type FormEvent } from 'react'
import { Link } from 'react-router-dom'
import { api } from '../api/client'

// /forgot — pede o email pro user. Backend SEMPRE responde 202
// (anti-enumeration), então mostramos a mesma mensagem de sucesso
// independentemente do email existir ou não.

export default function ForgotPassword() {
  const [email, setEmail] = useState('')
  const [sent, setSent] = useState(false)
  const [submitting, setSubmitting] = useState(false)

  async function handleSubmit(e: FormEvent) {
    e.preventDefault()
    setSubmitting(true)
    try {
      await api.post('/api/v1/forgot-password', { email })
    } catch {
      // Mesmo erro de rede: front mostra "enviamos se a conta existe"
      // pra preservar o anti-enumeration. Em produção, retentos no
      // backend cuidariam de entregar mesmo se o SMTP estiver lento.
    } finally {
      setSent(true)
      setSubmitting(false)
    }
  }

  return (
    <div className="mx-auto max-w-sm mt-12 p-6">
      <div className="bg-parchment border-4 border-ink shadow-pixel">
        <h1 className="bg-arcane text-parchment text-sm font-bold uppercase tracking-widest border-b-4 border-ink px-4 py-3">
          ▶ Esqueci minha senha
        </h1>

        {sent ? (
          <div className="p-6 space-y-4">
            <p className="text-sm leading-relaxed">
              Se houver uma conta com este email, enviamos um link de
              redefinição. Verifique sua caixa de entrada e a pasta
              de spam.
            </p>
            <Link
              to="/login"
              className="
                inline-block bg-ink text-parchment border-4 border-ink shadow-pixel
                px-4 py-2 text-xs font-bold uppercase tracking-widest
                transition-all duration-75 ease-out
                hover:translate-x-[2px] hover:translate-y-[2px] hover:shadow-none
              "
            >
              ▶ Voltar ao login
            </Link>
          </div>
        ) : (
          <form onSubmit={handleSubmit} className="p-6 space-y-4">
            <p className="text-xs uppercase tracking-widest text-ink/70 mb-2">
              ▸ Informe seu email e enviamos um link pra trocar a senha.
            </p>
            <label className="block space-y-1">
              <span className="text-xs uppercase tracking-widest font-bold text-ink/70">
                Email
              </span>
              <input
                type="email"
                value={email}
                onChange={(e) => setEmail(e.target.value)}
                required
                autoFocus
                className="w-full bg-parchment border-2 border-ink px-3 py-2 text-sm focus:outline-none focus:bg-arcane/10"
                placeholder="você@email.com"
              />
            </label>
            <button
              type="submit"
              disabled={submitting}
              className="
                w-full bg-arcane text-parchment border-4 border-ink shadow-pixel
                px-4 py-3 text-sm font-bold uppercase tracking-widest
                transition-all duration-75 ease-out
                hover:translate-x-[2px] hover:translate-y-[2px] hover:shadow-none
                disabled:opacity-50 disabled:hover:translate-x-0 disabled:hover:translate-y-0 disabled:hover:shadow-pixel
              "
            >
              {submitting ? '...' : '▶ Enviar link'}
            </button>
            <Link
              to="/login"
              className="block text-center text-xs uppercase tracking-widest font-bold underline underline-offset-4 decoration-2 text-ink/60 hover:text-arcane"
            >
              ◀ Voltar ao login
            </Link>
          </form>
        )}
      </div>
    </div>
  )
}
