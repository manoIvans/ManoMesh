import { lazy, Suspense } from 'react'
import { Route, Routes } from 'react-router-dom'
import Layout from './components/Layout'
import ScrollToTop from './components/ScrollToTop'
import Gallery from './pages/Gallery'
import NotFound from './pages/NotFound'
import AuthInterceptor from './auth/AuthInterceptor'
import { ProtectedRoute } from './auth/ProtectedRoute'
import { ErrorBoundary } from './components/ErrorBoundary'

// Code splitting: Login, Dashboard e Viewer só carregam quando o
// usuário navega pra essas rotas. A Galeria fica eager porque é o
// destino do path "/" — o usuário SEMPRE entra por ela, lazy não
// traria ganho.
//
// Viewer é especialmente importante manter LAZY: ele puxa three.js +
// R3F + drei (~300 kB minified). Não pode entrar no chunk inicial sob
// nenhuma circunstância — quem só abre a galeria não deve baixar isso.
//
// Vite gera chunks separados a partir desses imports dinâmicos
// automaticamente — sem precisar configurar manualSplits.
const Login = lazy(() => import('./pages/Login'))
const ForgotPassword = lazy(() => import('./pages/ForgotPassword'))
const ResetPassword = lazy(() => import('./pages/ResetPassword'))
const VerifyEmail = lazy(() => import('./pages/VerifyEmail'))
const Dashboard = lazy(() => import('./pages/Dashboard'))
const Viewer = lazy(() => import('./pages/Viewer'))
const AssetDetail = lazy(() => import('./pages/AssetDetail'))
const AssetEdit = lazy(() => import('./pages/AssetEdit'))
const MyStore = lazy(() => import('./pages/MyStore'))
const Library = lazy(() => import('./pages/Library'))
const ProfileMe = lazy(() => import('./pages/ProfileMe'))
const UserProfile = lazy(() => import('./pages/UserProfile'))
const Favorites = lazy(() => import('./pages/Favorites'))
const Cart = lazy(() => import('./pages/Cart'))
const Checkout = lazy(() => import('./pages/Checkout'))
const Packs = lazy(() => import('./pages/Packs'))
const PackDetail = lazy(() => import('./pages/PackDetail'))
const PackNew = lazy(() => import('./pages/PackNew'))
const PackEdit = lazy(() => import('./pages/PackEdit'))
const Creators = lazy(() => import('./pages/Creators'))
const Notifications = lazy(() => import('./pages/Notifications'))

// Fallback minimalista enquanto o chunk da rota carrega. Vazio de
// propósito: em rede normal o chunk vem em ~50-100ms e qualquer
// spinner aqui só "pisca" e atrapalha mais que ajuda. Se a rede
// piorar muito, troca por um skeleton específico da rota.
const RouteFallback = () => null

export default function App() {
  return (
    <ErrorBoundary>
      {/* AuthInterceptor: registra o handler global de 401. Não
          renderiza nada visível — só conecta o api/client ao
          AuthContext + useNavigate. Precisa estar DENTRO do Router. */}
      <AuthInterceptor />
      {/* ScrollToTop: reseta scroll ao topo em mudança de pathname.
          Filtros da galeria (search params) NÃO disparam — só
          navegação real entre páginas. */}
      <ScrollToTop />
      <Routes>
        <Route element={<Layout />}>
          <Route path="/" element={<Gallery />} />
          <Route
            path="/login"
            element={
              <Suspense fallback={<RouteFallback />}>
                <Login />
              </Suspense>
            }
          />
          {/* Recuperação de senha. /forgot pede email; /reset
              consome token (vem do link no email). Públicas — o
              próprio token é a credencial. */}
          <Route
            path="/forgot"
            element={
              <Suspense fallback={<RouteFallback />}>
                <ForgotPassword />
              </Suspense>
            }
          />
          <Route
            path="/reset"
            element={
              <Suspense fallback={<RouteFallback />}>
                <ResetPassword />
              </Suspense>
            }
          />
          {/* /verify?token=... confirma email. Pode ser acessada
              logado (refresca currentUser) ou deslogado (mostra
              sucesso/erro e oferece login). */}
          <Route
            path="/verify"
            element={
              <Suspense fallback={<RouteFallback />}>
                <VerifyEmail />
              </Suspense>
            }
          />
          <Route
            path="/dashboard"
            element={
              <ProtectedRoute>
                <Suspense fallback={<RouteFallback />}>
                  <Dashboard />
                </Suspense>
              </ProtectedRoute>
            }
          />
          {/* /viewer: rota de teste para o ModelViewer. Publica para
              facilitar a validação visual do pipeline 3D; quando virar
              parte do detalhe de asset, pode sair. */}
          <Route
            path="/viewer"
            element={
              <Suspense fallback={<RouteFallback />}>
                <Viewer />
              </Suspense>
            }
          />
          {/* /asset/:id: página de detalhe pública. Lazy porque
              compartilha o mesmo bundle pesado do ModelViewer (R3F). */}
          <Route
            path="/asset/:id"
            element={
              <Suspense fallback={<RouteFallback />}>
                <AssetDetail />
              </Suspense>
            }
          />
          {/* /asset/:id/edit: form de edição do dono. Protegida
              porque PUT exige JWT; o próprio AssetEdit ainda redireciona
              não-donos pra rota de leitura. */}
          <Route
            path="/asset/:id/edit"
            element={
              <ProtectedRoute>
                <Suspense fallback={<RouteFallback />}>
                  <AssetEdit />
                </Suspense>
              </ProtectedRoute>
            }
          />
          {/* /my-store: vitrine dos assets do dono. Protegida — só
              faz sentido quando há usuário logado pra filtrar. */}
          <Route
            path="/my-store"
            element={
              <ProtectedRoute>
                <Suspense fallback={<RouteFallback />}>
                  <MyStore />
                </Suspense>
              </ProtectedRoute>
            }
          />
          {/* /library: stub do "comprados". Mesmo sem dados, ficar
              protegida deixa explícito que é página pessoal. */}
          <Route
            path="/library"
            element={
              <ProtectedRoute>
                <Suspense fallback={<RouteFallback />}>
                  <Library />
                </Suspense>
              </ProtectedRoute>
            }
          />
          {/* /perfil/me: editar próprio perfil. Protegida porque
              consome /users/me (precisa JWT). */}
          <Route
            path="/perfil/me"
            element={
              <ProtectedRoute>
                <Suspense fallback={<RouteFallback />}>
                  <ProfileMe />
                </Suspense>
              </ProtectedRoute>
            }
          />
          {/* /u/:username: perfil PÚBLICO. Visível sem login. */}
          <Route
            path="/u/:username"
            element={
              <Suspense fallback={<RouteFallback />}>
                <UserProfile />
              </Suspense>
            }
          />
          {/* /criadores: diretório público de todos os usuários. */}
          <Route
            path="/criadores"
            element={
              <Suspense fallback={<RouteFallback />}>
                <Creators />
              </Suspense>
            }
          />
          {/* /favoritos: assets salvos pelo usuário. Protegida —
              precisa JWT pra /my/favorites. */}
          <Route
            path="/favoritos"
            element={
              <ProtectedRoute>
                <Suspense fallback={<RouteFallback />}>
                  <Favorites />
                </Suspense>
              </ProtectedRoute>
            }
          />
          {/* /carrinho: revisão antes do checkout. Protegida. */}
          <Route
            path="/carrinho"
            element={
              <ProtectedRoute>
                <Suspense fallback={<RouteFallback />}>
                  <Cart />
                </Suspense>
              </ProtectedRoute>
            }
          />
          {/* /packs: catálogo público de packs (bundles). */}
          <Route
            path="/packs"
            element={
              <Suspense fallback={<RouteFallback />}>
                <Packs />
              </Suspense>
            }
          />
          {/* /pack/:id: detalhe público + add ao carrinho. */}
          <Route
            path="/pack/:id"
            element={
              <Suspense fallback={<RouteFallback />}>
                <PackDetail />
              </Suspense>
            }
          />
          {/* /dashboard/packs/new: form de criação do vendedor. */}
          <Route
            path="/dashboard/packs/new"
            element={
              <ProtectedRoute>
                <Suspense fallback={<RouteFallback />}>
                  <PackNew />
                </Suspense>
              </ProtectedRoute>
            }
          />
          {/* /dashboard/packs/:id/edit: form de edição. Ownership
              é validada client-side (redireciona pro detalhe) E
              server-side (PUT devolve 403). */}
          <Route
            path="/dashboard/packs/:id/edit"
            element={
              <ProtectedRoute>
                <Suspense fallback={<RouteFallback />}>
                  <PackEdit />
                </Suspense>
              </ProtectedRoute>
            }
          />
          {/* /checkout/:sessionId: STUB do provedor de pagamento.
              Mostra total + botão "Pagar" que chama POST /confirm.
              Em produção real, esta rota some — usuário sai do app
              pro Stripe/MP e volta via webhook. */}
          <Route
            path="/checkout/:sessionId"
            element={
              <ProtectedRoute>
                <Suspense fallback={<RouteFallback />}>
                  <Checkout />
                </Suspense>
              </ProtectedRoute>
            }
          />
          {/* /notificacoes: lista in-app das últimas notificações. */}
          <Route
            path="/notificacoes"
            element={
              <ProtectedRoute>
                <Suspense fallback={<RouteFallback />}>
                  <Notifications />
                </Suspense>
              </ProtectedRoute>
            }
          />
          {/* Catch-all: caminho desconhecido cai no NotFound dedicado
              em vez do redirect silencioso pra galeria — ajuda debug
              de link quebrado e dá contexto pro usuário. */}
          <Route path="*" element={<NotFound />} />
        </Route>
      </Routes>
    </ErrorBoundary>
  )
}
