import {
  createContext,
  useCallback,
  useContext,
  useEffect,
  useMemo,
  useState,
  type ReactNode,
} from 'react'
import { api, type User } from '../api/client'
import { decodeJwt } from './jwt'
import { tokenStorage } from './tokenStorage'

type AuthState = {
  token: string | null
  // currentUserId é derivado do token via decode local. Pode ser null
  // se o token estiver malformado ou ausente. NÃO confiar para
  // autorização — só pra mostrar/esconder UI; o backend valida tudo.
  currentUserId: number | null
  isAuthenticated: boolean
  // currentUser carrega o perfil completo (display_name, avatar_path,
  // bio, email) buscado em GET /users/me sempre que o token muda.
  // Pode ser null em 3 cenários:
  //   1. Não autenticado.
  //   2. Autenticado mas a request ainda está voando (loading).
  //   3. Falhou (rede off, 401 → logout automático).
  // O header lida com isso renderizando placeholder/skeleton.
  currentUser: User | null
  // login aceita o par (access+refresh) recém-emitido pelo backend.
  // Grava ambos no tokenStorage; o api/client usa o access em todas
  // as requests, o refresh é consumido só em refresh-on-401.
  login: (accessToken: string, refreshToken: string) => void
  // logout opcionalmente faz POST /logout no backend pra revogar o
  // refresh. Falha não bloqueia — o cliente já apaga os dois localmente.
  logout: () => Promise<void>
  // refreshUser força um GET /users/me. Útil depois de PATCH ou
  // upload de avatar — quem chamou o mutation pode invalidar a
  // cópia local de currentUser sem precisar fazer o fetch manual.
  refreshUser: () => Promise<void>
}

const AuthContext = createContext<AuthState | null>(null)

// AuthProvider mantém o token em estado React e em localStorage.
// Inicializa de forma SÍNCRONA a partir do storage para evitar o
// flash de "deslogado → logado" no primeiro render quando o usuário
// já tem um token salvo.
//
// Não decodificamos o JWT pra checar expiração: o backend responde
// 401 em token expirado, e o helper de API força logout no primeiro
// 401. Mantém a lógica em um lugar só.
export function AuthProvider({ children }: { children: ReactNode }) {
  const [token, setToken] = useState<string | null>(() => tokenStorage.get())
  const [currentUser, setCurrentUser] = useState<User | null>(null)

  const login = useCallback(
    (accessToken: string, refreshToken: string) => {
      tokenStorage.setPair(accessToken, refreshToken)
      setToken(accessToken)
    },
    [],
  )

  const logout = useCallback(async () => {
    const refresh = tokenStorage.getRefresh()
    // Tenta revogar no backend best-effort. Falha (rede, 4xx) não
    // bloqueia o logout local — token vira lixo no DB do servidor
    // mas access expira sozinho em 1h.
    tokenStorage.clear()
    setToken(null)
    setCurrentUser(null)
    if (refresh) {
      try {
        await api.post('/api/v1/logout', { refresh_token: refresh })
      } catch {
        // silencioso — logout client-side já aconteceu
      }
    }
  }, [])

  // refreshUser: buscar /users/me. Stand-alone callback pra que outras
  // partes da app possam disparar (após PATCH/upload). O effect abaixo
  // chama isso quando o token muda.
  const refreshUser = useCallback(async () => {
    if (!tokenStorage.get()) {
      setCurrentUser(null)
      return
    }
    try {
      const user = await api.get<User>('/api/v1/users/me')
      setCurrentUser(user)
    } catch {
      // 401 (token expirado) → logout. Outros erros (rede off) só
      // deixam currentUser null; o header vira placeholder. O próximo
      // refresh tenta de novo.
      setCurrentUser(null)
    }
  }, [])

  // Carrega o perfil sempre que o token muda (login, logout, refresh
  // da página com token salvo). Ignora a Promise resolvida — refreshUser
  // já trata erros internamente.
  useEffect(() => {
    if (token === null) {
      setCurrentUser(null)
      return
    }
    void refreshUser()
  }, [token, refreshUser])

  // currentUserId computado a partir do token. useMemo evita
  // re-decodificar a cada render quando o token não muda.
  //
  // Campo `uid` (não user_id) — convenção definida pelo backend Go
  // em internal/auth/jwt.go.
  const currentUserId = useMemo<number | null>(() => {
    if (!token) return null
    const payload = decodeJwt(token)
    return typeof payload?.uid === 'number' ? payload.uid : null
  }, [token])

  // useMemo evita recriar o objeto a cada render. Sem isso, todo
  // consumidor de useAuth renderiza desnecessariamente.
  const value = useMemo<AuthState>(
    () => ({
      token,
      currentUserId,
      currentUser,
      isAuthenticated: token !== null,
      login,
      logout,
      refreshUser,
    }),
    [token, currentUserId, currentUser, login, logout, refreshUser],
  )

  return <AuthContext.Provider value={value}>{children}</AuthContext.Provider>
}

export function useAuth(): AuthState {
  const ctx = useContext(AuthContext)
  if (!ctx) {
    throw new Error('useAuth precisa estar dentro de <AuthProvider>')
  }
  return ctx
}
