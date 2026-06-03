import { Link, NavLink, Outlet, useNavigate } from 'react-router-dom'
import { useAuth } from '../auth/AuthContext'
import { useCart } from '../cart/CartContext'
import { useNotifications } from '../notifications/NotificationsContext'
import Avatar from './Avatar'
import VerifyEmailBanner from './VerifyEmailBanner'

// Shell de todas as páginas. A combinação bg-parchment + text-ink +
// font-mono é aplicada no root para que TODA descendência herde a
// estética RPG sem precisar repetir as classes em cada página.
//
// Header é uma "title bar" de jogo:
//   - Linha principal roxo arcano com a marca + nav links
//   - Faixa de madeira embaixo dando um efeito de "moldura"
//   - Item de rota ATIVA recebe cores invertidas (parchment + ink),
//     replicando o feel de "menu item selecionado" em RPGs antigos.

// Função de classes para NavLink. NavLink chama isso a cada render
// passando isActive — TS infere o tipo do argumento, mas ser explícito
// melhora a legibilidade.
function navLinkClasses({ isActive }: { isActive: boolean }) {
  // Borda 2px sempre presente (transparent quando inativo) para que o
  // layout NÃO mude de tamanho quando o estado ativo entra/sai. Sem
  // isso, os links pulam de posição na navegação.
  const base =
    'inline-block px-3 py-1 text-xs uppercase tracking-widest font-bold border-2 transition-colors duration-75'
  return isActive
    ? `${base} bg-parchment text-ink border-ink`
    : `${base} border-transparent text-parchment hover:bg-ink/30`
}

export default function Layout() {
  const { isAuthenticated, currentUser, logout } = useAuth()
  const { count: cartCount } = useCart()
  const { unreadCount } = useNotifications()
  const navigate = useNavigate()

  function handleLogout() {
    // logout virou async (revoga refresh no backend), mas não esperamos
    // — o navigate já tira o user da tela e o tokenStorage já foi limpo
    // sincronamente. Falha do POST /logout é silenciosa.
    void logout()
    navigate('/login', { replace: true })
  }

  return (
    <div className="min-h-screen flex flex-col bg-twilight-scan text-ink font-mono">
      {/* Header sticky: gruda no topo durante o scroll. z-30 fica acima
          do conteúdo da página (cards, viewer 3D) mas abaixo de toasts
          (z-50). bg-arcane-magic é opaco — não vaza conteúdo por trás. */}
      <header className="sticky top-0 z-30">
        <nav className="bg-arcane-magic text-parchment px-6 py-4 border-b-4 border-ink flex items-center gap-4">
          {/* Marca em duas linhas: nome principal + tagline minúscula
              abaixo, no esquema "logo de jogo retrô". */}
          <Link
            to="/"
            className="flex flex-col gap-1.5 mr-2 group leading-none"
            aria-label="Página inicial"
          >
            {/* font-pixel = Press Start 2P. Removidos font-bold (a fonte
                bitmap não tem variações de peso) e tracking custom (já
                vem espaçada). Mantive uppercase como segurança caso o
                texto futuro tenha minúsculas — a fonte só tem caixa alta. */}
            <span className="font-pixel text-sm uppercase">ManoMesh</span>
            <span className="text-[9px] uppercase tracking-[0.4em] text-parchment/60">
              Assets Raros
            </span>
          </Link>

          {/* Separador vertical com baixa opacidade — divide marca do menu */}
          <span aria-hidden="true" className="text-parchment/30 select-none">
            │
          </span>

          {/* NavLink (vs Link) habilita o estado isActive para mostrar
              a rota atual. `end` no link "/" evita que ele fique ativo
              em todas as rotas (já que "/" é prefixo de tudo). */}
          <NavLink to="/" end className={navLinkClasses}>
            Galeria
          </NavLink>
          {/* Criadores fica fora do bloco autenticado — diretório
              público, qualquer visitante pode explorar. */}
          <NavLink to="/criadores" className={navLinkClasses}>
            Criadores
          </NavLink>
          <NavLink to="/packs" className={navLinkClasses}>
            Packs
          </NavLink>
          {isAuthenticated && (
            <>
              <NavLink to="/my-store" className={navLinkClasses}>
                Minha Loja
              </NavLink>
              <NavLink to="/favoritos" className={navLinkClasses}>
                Favoritos
              </NavLink>
              <NavLink to="/carrinho" className={navLinkClasses}>
                Carrinho
                {/* Badge com count só aparece quando > 0 pra não poluir.
                    Renderizado dentro do NavLink pra ficar relativo
                    ao link sem precisar de wrapper extra. */}
                {cartCount > 0 && (
                  <span className="ml-1 inline-block bg-arcane text-parchment border border-current px-1 text-[9px]">
                    {cartCount}
                  </span>
                )}
              </NavLink>
              <NavLink to="/library" className={navLinkClasses}>
                Biblioteca
              </NavLink>
              <NavLink to="/dashboard" className={navLinkClasses}>
                Dashboard
              </NavLink>
              {/* Bell: leva pra /notificacoes, badge mostra contagem
                  de não-lidas. Some o badge quando 0 ou undefined
                  (ainda carregando). */}
              <NavLink
                to="/notificacoes"
                className={navLinkClasses}
                aria-label={
                  unreadCount && unreadCount > 0
                    ? `${unreadCount} notificações não lidas`
                    : 'Notificações'
                }
              >
                <span aria-hidden="true">◔</span>
                {unreadCount !== undefined && unreadCount > 0 && (
                  <span className="ml-1 inline-block bg-arcane text-parchment border border-current px-1 text-[9px]">
                    {unreadCount}
                  </span>
                )}
              </NavLink>
            </>
          )}

          {/* Bloco de identidade à direita: quando logado, mostra
              avatar+nome (linka pra /perfil/me) e botão Sair. Quando
              deslogado, só o botão Entrar.

              currentUser pode estar null mesmo com isAuthenticated=true
              num breve intervalo entre login e a primeira request de
              /users/me terminar — nesse caso, escondemos só o nome e
              o avatar fica como fallback "?", evitando layout shift. */}
          <div className="ml-auto flex items-center gap-3">
            {isAuthenticated ? (
              <>
                <Link
                  to="/perfil/me"
                  aria-label="Meu perfil"
                  className="group flex items-center gap-2"
                >
                  <Avatar
                    avatarPath={currentUser?.avatar_path}
                    name={currentUser?.display_name ?? '?'}
                    size="sm"
                    interactive
                  />
                  {/* Nome aparece só em telas médias+ pra economizar
                      espaço no mobile (o avatar já é o anchor visual). */}
                  <span className="hidden sm:inline text-xs uppercase tracking-widest font-bold group-hover:underline underline-offset-4 decoration-2">
                    {currentUser?.display_name ?? '...'}
                  </span>
                </Link>
                <button
                  onClick={handleLogout}
                  className="
                    inline-block px-3 py-1.5 text-xs uppercase tracking-widest font-bold
                    border-2 border-parchment text-parchment
                    transition-colors duration-75
                    hover:bg-parchment hover:text-arcane
                  "
                >
                  ▶ Sair
                </button>
              </>
            ) : (
              <NavLink
                to="/login"
                className={({ isActive }) =>
                  `inline-block px-3 py-1.5 text-xs uppercase tracking-widest font-bold border-2 transition-colors duration-75 ${
                    isActive
                      ? 'bg-parchment text-arcane border-parchment'
                      : 'border-parchment text-parchment hover:bg-parchment hover:text-arcane'
                  }`
                }
              >
                ▶ Entrar
              </NavLink>
            )}
          </div>
        </nav>

        {/* Banner de email não verificado: aparece dentro do header
            (acima do main) pra ficar visível sempre, sem ser parte
            da página em si. Esconde sozinho quando email_verified_at
            não é null no currentUser. */}
        <VerifyEmailBanner />
      </header>

      <main className="flex-1">
        <Outlet />
      </main>
    </div>
  )
}
