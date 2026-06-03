// Token storage isolado em um módulo próprio porque DUAS coisas
// precisam ler/escrever o mesmo valor: o AuthContext (React) e a
// API helper (não-React). Manter o localStorage como fonte da verdade
// evita os dois divergirem.
//
// Desde a Fase de auth hygiene, armazenamos um PAR de tokens:
//   - access  (JWT curto, 1h)  — vai no header Authorization
//   - refresh (opaque, 30d)    — usado pra rotacionar o par via /refresh
//
// O par é gravado/lido junto. Quando o user faz logout (ou /refresh
// detecta reuso), zeramos os dois.

const ACCESS_KEY = 'lojinha:token'         // Mantido pra compat com sessões antigas.
const REFRESH_KEY = 'lojinha:refresh-token'

export const tokenStorage = {
  get(): string | null {
    return localStorage.getItem(ACCESS_KEY)
  },
  set(token: string): void {
    localStorage.setItem(ACCESS_KEY, token)
  },
  getRefresh(): string | null {
    return localStorage.getItem(REFRESH_KEY)
  },
  setRefresh(token: string): void {
    localStorage.setItem(REFRESH_KEY, token)
  },
  // setPair grava os dois numa única operação — usado por
  // Login/Register/Refresh pra que o pareamento fique consistente.
  setPair(access: string, refresh: string): void {
    localStorage.setItem(ACCESS_KEY, access)
    localStorage.setItem(REFRESH_KEY, refresh)
  },
  clear(): void {
    localStorage.removeItem(ACCESS_KEY)
    localStorage.removeItem(REFRESH_KEY)
  },
}
