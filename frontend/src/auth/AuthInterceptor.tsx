import { useEffect } from 'react'
import { useLocation, useNavigate } from 'react-router-dom'
import { setOnUnauthorized } from '../api/client'
import { useAuth } from './AuthContext'

// AuthInterceptor: registra o callback global de 401 do api/client.
//
// Por que componente em vez de chamar setOnUnauthorized direto no
// AuthProvider:
//   - Precisa de useNavigate, que só funciona DENTRO do Router.
//     AuthProvider envolve o Router (não está dentro dele) — uma
//     limitação que vem de queremos auth state disponível ANTES do
//     primeiro render de rotas.
//   - Componente filho do <Routes> consegue ambos. Não renderiza
//     nada (return null) — é só um hook holder.
//
// Limpeza: o useEffect retorna unsubscribe pra que, em hot reload,
// não fiquem múltiplos callbacks empilhados disparando logout 2x.
export default function AuthInterceptor() {
  const { logout } = useAuth()
  const navigate = useNavigate()
  const location = useLocation()

  useEffect(() => {
    setOnUnauthorized(() => {
      // Já estamos no /login? Não faz nada — provavelmente é o
      // próprio submit que falhou. Evita loop de "navigate(/login)
      // → form submit → 401 → navigate(/login)".
      if (location.pathname === '/login') return

      // logout virou async (chama /logout pra revogar refresh server-side);
      // não esperamos o Promise — o redirect deve acontecer imediatamente.
      void logout()
      // state.sessionExpired: o Login lê pra mostrar banner explicando.
      // from: pra que após login bem-sucedido o usuário volte pra
      // página que estava tentando acessar.
      navigate('/login', {
        replace: true,
        state: {
          from: location,
          sessionExpired: true,
        },
      })
    })

    return () => {
      setOnUnauthorized(null)
    }
  }, [logout, navigate, location])

  return null
}
