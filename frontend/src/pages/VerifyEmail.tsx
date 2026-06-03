import { useEffect, useState } from 'react'
import { Link, useNavigate, useSearchParams } from 'react-router-dom'
import { ApiError, api } from '../api/client'
import { useAuth } from '../auth/AuthContext'

// /verify?token=... — confirma o email. Auto-submita no mount; mostra
// "verificado!" em sucesso e redireciona pra home depois de 2s. Se já
// estiver logado, dispara um refresh do currentUser pra que o banner
// "confirme seu email" suma sem precisar relogar.

type State = 'idle' | 'success' | 'invalid' | 'expired' | 'error'

export default function VerifyEmail() {
  const [params] = useSearchParams()
  const navigate = useNavigate()
  const { refreshUser } = useAuth()
  const token = params.get('token') ?? ''

  const [state, setState] = useState<State>('idle')

  useEffect(() => {
    if (!token) {
      setState('invalid')
      return
    }
    let cancelled = false
    api
      .post('/api/v1/verify-email', { token })
      .then(async () => {
        if (cancelled) return
        setState('success')
        // Re-fetch /users/me pra que email_verified_at apareça no
        // currentUser e o banner suma. Silencioso — falha não afeta
        // a tela de "verificado".
        try {
          await refreshUser()
        } catch {
          // ignore
        }
        // Redirect automático em 2s pra dar sinal visual antes.
        setTimeout(() => {
          if (!cancelled) navigate('/', { replace: true })
        }, 2000)
      })
      .catch((err) => {
        if (cancelled) return
        if (err instanceof ApiError) {
          if (err.status === 400) {
            setState('invalid')
            return
          }
          if (err.status === 410) {
            setState('expired')
            return
          }
        }
        setState('error')
      })
    return () => {
      cancelled = true
    }
  }, [token, navigate, refreshUser])

  return (
    <div className="mx-auto max-w-sm mt-12 p-6">
      <div className="bg-parchment border-4 border-ink shadow-pixel">
        <h1 className="bg-arcane text-parchment text-sm font-bold uppercase tracking-widest border-b-4 border-ink px-4 py-3">
          ▶ Verificação de email
        </h1>
        <div className="p-6 space-y-4 text-sm">
          {state === 'idle' && (
            <p className="uppercase tracking-widest text-ink/70 font-bold">
              ▌ Validando token...
            </p>
          )}
          {state === 'success' && (
            <>
              <p className="uppercase tracking-widest font-bold text-arcane">
                ✓ Email verificado!
              </p>
              <p className="text-xs text-ink/70">
                Redirecionando pra galeria...
              </p>
            </>
          )}
          {state === 'invalid' && (
            <>
              <p className="font-bold">Token inválido</p>
              <p className="text-xs text-ink/70">
                Pode ter sido usado antes ou estar com erro de
                digitação. Faça login e peça um novo link no banner
                do topo.
              </p>
              <Link
                to="/login"
                className="inline-block bg-ink text-parchment border-4 border-ink shadow-pixel px-4 py-2 text-xs font-bold uppercase tracking-widest hover:translate-x-[2px] hover:translate-y-[2px] hover:shadow-none transition-all duration-75 ease-out"
              >
                ▶ Login
              </Link>
            </>
          )}
          {state === 'expired' && (
            <>
              <p className="font-bold">Token expirou</p>
              <p className="text-xs text-ink/70">
                Tokens valem por 24 horas. Faça login e peça um novo
                link no banner do topo.
              </p>
              <Link
                to="/login"
                className="inline-block bg-ink text-parchment border-4 border-ink shadow-pixel px-4 py-2 text-xs font-bold uppercase tracking-widest hover:translate-x-[2px] hover:translate-y-[2px] hover:shadow-none transition-all duration-75 ease-out"
              >
                ▶ Login
              </Link>
            </>
          )}
          {state === 'error' && (
            <p className="font-bold">Falha ao validar — tente novamente em alguns minutos.</p>
          )}
        </div>
      </div>
    </div>
  )
}
