import { useCallback, useEffect, useMemo, useState } from 'react'
import { Link, useNavigate, useParams } from 'react-router-dom'
import {
  ApiError,
  api,
  fileUrl,
  type Asset,
  type Pack,
  type Review,
  type ReviewSummary,
} from '../api/client'
import { useAuth } from '../auth/AuthContext'
import { formatDate, formatPrice } from '../lib/format'
import AssetCard from '../components/AssetCard'
import AssetCardSkeleton from '../components/AssetCardSkeleton'
import Avatar from '../components/Avatar'
import CartButton from '../components/CartButton'
import FavoriteButton from '../components/FavoriteButton'
import StarRating from '../components/StarRating'
import { useToast } from '../components/Toast'
import ModelViewer from '../components/ModelViewer'

// AssetDetail (/asset/:id): página de detalhe do asset.
//
// Layout em três blocos verticais:
//   1. Back link (◀ Voltar à galeria) — pequeno, no topo
//   2. Hero card — título grande + autor + #id + data
//   3. Grid 2/3 + 1/3 (no desktop) — Viewer + Info
//   4. (SE for dono) OwnerPanel com botões de editar/excluir
//
// Três variáveis de estado de propósito não-mutuamente-exclusivas:
//   - loading: fetch em andamento
//   - notFound: 404 do backend (asset deletado ou nunca existiu)
//   - error: rede/5xx/etc
// Diferenciar 404 de erro genérico permite copy distinto.
export default function AssetDetail() {
  const { id } = useParams<{ id: string }>()
  const [asset, setAsset] = useState<Asset | null>(null)
  const [loading, setLoading] = useState(true)
  const [notFound, setNotFound] = useState(false)
  const [error, setError] = useState<string | null>(null)

  useEffect(() => {
    if (!id) {
      setNotFound(true)
      setLoading(false)
      return
    }

    let cancelled = false
    setLoading(true)
    setNotFound(false)
    setError(null)

    api
      .get<Asset>(`/api/v1/assets/${id}`)
      .then((a) => {
        if (cancelled) return
        setAsset(a)
        setLoading(false)
      })
      .catch((err) => {
        if (cancelled) return
        if (err instanceof ApiError && err.status === 404) {
          setNotFound(true)
        } else {
          setError('Falha ao carregar o asset')
        }
        setLoading(false)
      })

    return () => {
      cancelled = true
    }
  }, [id])

  if (loading) return <LoadingState />
  if (notFound) return <NotFoundState />
  if (error || !asset)
    return <ErrorState message={error ?? 'Erro desconhecido'} />

  return <Detail asset={asset} />
}

function Detail({ asset }: { asset: Asset }) {
  const { currentUserId } = useAuth()
  const navigate = useNavigate()
  const toast = useToast()

  // Comparação só faz sentido se o usuário estiver logado E o asset
  // tiver owner_id (que sempre tem). isOwner controla a renderização
  // do OwnerPanel — backend ainda valida ownership em toda mutação
  // de qualquer jeito.
  const isOwner =
    currentUserId !== null && currentUserId === asset.owner_id

  async function handleDelete() {
    try {
      await api.delete(`/api/v1/assets/${asset.id}`)
      // Toast antes do navigate: o ToastProvider vive ACIMA das rotas,
      // então sobrevive à desmontagem dessa página.
      toast.success(`"${asset.title}" excluído`)
      navigate('/', { replace: true })
    } catch (err) {
      // OwnerPanel devolve o usuário ao estado pré-confirm via re-throw
      // (catch interno). Aqui só sinalizamos a falha pro usuário e
      // logamos pra debugar depois.
      console.error('delete asset:', err)
      toast.error(messageForDelete(err))
      throw err
    }
  }

  return (
    <div className="max-w-7xl mx-auto p-6 space-y-6">
      <Link
        to="/"
        className="inline-block text-xs font-bold uppercase tracking-widest text-parchment hover:underline underline-offset-4 decoration-2"
      >
        ◀ Voltar à galeria
      </Link>

      <header className="bg-parchment border-4 border-ink shadow-pixel">
        <p className="bg-arcane text-parchment font-pixel text-xs uppercase border-b-4 border-ink px-4 py-3">
          ▶ Asset #{asset.id}
        </p>
        <div className="px-6 py-5 space-y-3">
          <h1 className="text-2xl md:text-3xl font-bold uppercase tracking-wider break-words leading-tight">
            {asset.title}
          </h1>
          {/* Resumo de reviews: estrelas + total. Próprio componente
              gerencia fetch + esconde quando count = 0. */}
          <ReviewSummaryInline assetID={asset.id} />
          {/* Linha do autor com avatar + link clicável pro perfil
              público. Quando author_username está presente (sempre,
              em respostas com JOIN), vira <Link>; quando ausente
              (legacy/edge case), fica só texto. */}
          <div className="flex flex-wrap items-center gap-3">
            <Avatar
              avatarPath={asset.author_avatar_path}
              name={asset.author_name ?? '?'}
              size="sm"
            />
            <div className="text-xs uppercase tracking-widest flex flex-wrap items-center gap-x-3 gap-y-1">
              {asset.author_username ? (
                <Link
                  to={`/u/${asset.author_username}`}
                  className="font-bold hover:underline underline-offset-4 decoration-2 hover:text-arcane"
                >
                  {asset.author_name ?? asset.author_username}
                </Link>
              ) : (
                <span className="font-bold">
                  {asset.author_name ?? 'anônimo'}
                </span>
              )}
              <span aria-hidden="true" className="text-ink/30">
                │
              </span>
              <span>{formatDate(asset.created_at)}</span>
            </div>
          </div>
        </div>
      </header>

      {/* OwnerPanel acima do visualizador: o dono geralmente vem aqui
          pra editar/excluir, não pra rever o modelo — colocar antes
          do viewer evita scroll desnecessário. Backend continua
          validando ownership em PUT/DELETE (403 se não bater). */}
      {isOwner && <OwnerPanel asset={asset} onConfirmDelete={handleDelete} />}

      <div className="grid grid-cols-1 lg:grid-cols-3 gap-6">
        <section className="lg:col-span-2">
          <div className="bg-parchment border-4 border-ink shadow-pixel">
            <h2 className="bg-arcane text-parchment font-pixel text-xs uppercase border-b-4 border-ink px-4 py-3">
              ▶ Visualizador
            </h2>
            <div className="p-4">
              <ModelViewer
                modelUrl={fileUrl(asset.model_path)}
                className="w-full aspect-square bg-twilight border-4 border-ink"
              />
              <p className="mt-3 text-xs uppercase tracking-wider text-ink/70">
                ▌ Arraste pra orbitar · scroll pra zoom · botão direito
                pra mover
              </p>
            </div>
          </div>
        </section>

        <aside>
          <div className="bg-parchment border-4 border-ink shadow-pixel">
            <h2 className="bg-arcane text-parchment font-pixel text-xs uppercase border-b-4 border-ink px-4 py-3">
              ▶ Info
            </h2>

            <div className="p-6 space-y-5">
              <div className="bg-twilight text-parchment border-4 border-ink shadow-pixel-sm px-4 py-3 text-center">
                <p className="text-[10px] uppercase tracking-widest text-parchment/70">
                  Preço
                </p>
                <p className="text-2xl font-bold mt-1">
                  ✦ {formatPrice(asset.price_cents)}
                </p>
              </div>

              {/* Ações de compra/favorito. CartButton só pra
                  não-donos (backend rejeita auto-compra com 409;
                  esconder é UX mais limpa). FavoriteButton aparece
                  pra todos — favoritar próprio asset não é proibido. */}
              <div className="flex flex-wrap justify-center gap-2">
                {!isOwner && <CartButton assetID={asset.id} variant="inline" />}
                <FavoriteButton assetID={asset.id} variant="inline" />
              </div>

              <div>
                <h3 className="text-[10px] font-bold uppercase tracking-widest mb-2 text-ink/60">
                  ▸ Descrição
                </h3>
                <p className="text-sm leading-relaxed whitespace-pre-wrap">
                  {asset.description.trim() || (
                    <span className="text-ink/40">— sem descrição —</span>
                  )}
                </p>
              </div>

              {/* Tags multi-valor (migration 004). flex-wrap permite
                  quebrar linha em modelos com várias. Se vier vazio
                  (anomalia, schema garante NOT NULL DEFAULT '{}'),
                  mostramos placeholder discreto. */}
              <div>
                <h3 className="text-[10px] font-bold uppercase tracking-widest mb-2 text-ink/60">
                  ▸ Tags
                </h3>
                {asset.tags.length > 0 ? (
                  <div className="flex flex-wrap gap-2">
                    {asset.tags.map((tag) => (
                      <span
                        key={tag}
                        className="inline-block bg-arcane text-parchment text-[10px] px-2 py-1 uppercase tracking-widest font-bold"
                      >
                        {tag}
                      </span>
                    ))}
                  </div>
                ) : (
                  <p className="text-xs text-ink/40">— sem tags —</p>
                )}
              </div>
            </div>
          </div>
        </aside>
      </div>

      {/* Packs que contêm este asset (lookup inverso via
          GET /assets/:id/packs). Fetch separado do detalhe — falha
          silenciosa esconde a sessão; asset sem packs idem. */}
      <AssetPacksSection assetID={asset.id} />

      {/* Avaliações: lista pública + form de criar/editar pra quem
          comprou. Fetch separado do detalhe principal — falha não
          bloqueia render do asset. */}
      <ReviewsSection assetID={asset.id} currentUserId={currentUserId} />

      {/* Sessão de recomendações no fim da página: depois do usuário
          ter visto o asset, mostra outros com tags em comum.
          Fetch separado do AssetDetail principal pra não bloquear o
          render do detalhe quando o endpoint similar estiver lento. */}
      <SimilarSection assetID={asset.id} />
    </div>
  )
}

// ReviewSummaryInline: 1 linha com estrelas + "(N avaliações)" no
// header do AssetDetail. Esconde quando count=0 — asset sem reviews
// não deve poluir o header com "0 estrelas".
//
// Mantém fetch próprio (não compartilha com a lista abaixo) pra que
// o número apareça assim que possível, antes da lista carregar.
function ReviewSummaryInline({ assetID }: { assetID: number }) {
  const [summary, setSummary] = useState<ReviewSummary | null>(null)

  useEffect(() => {
    let cancelled = false
    setSummary(null)
    api
      .get<ReviewSummary>(`/api/v1/assets/${assetID}/reviews/summary`)
      .then((s) => {
        if (!cancelled) setSummary(s)
      })
      .catch(() => {
        // Falha silenciosa: bloco esconde com summary=null.
      })
    return () => {
      cancelled = true
    }
  }, [assetID])

  if (!summary || summary.count === 0) return null

  return (
    <div className="flex items-center gap-2 text-xs uppercase tracking-widest">
      <StarRating value={summary.average} size="sm" />
      <span className="font-bold">{summary.average.toFixed(1)}</span>
      <span className="text-ink/60">
        ({summary.count} {summary.count === 1 ? 'avaliação' : 'avaliações'})
      </span>
    </div>
  )
}

// ReviewsSection: lista pública de reviews + form pra quem pode
// avaliar. Lógica:
//   - Fetch GET /reviews em paralelo
//   - Se usuário logado: tenta achar o review dele na lista (filter
//     por user_id === currentUserId). Existe → modo "editar" no
//     form. Não existe → modo "criar".
//   - O backend valida "comprou pra avaliar" no POST. Frontend NÃO
//     pre-checa via /library-ids: complexidade extra, e o backend
//     rejeitar com 403 + toast é UX aceitável.
//
// Estados:
//   - reviews=null → loading skeleton
//   - reviews=[] → mensagem "Seja o primeiro a avaliar"
//   - reviews=[...] → lista
function ReviewsSection({
  assetID,
  currentUserId,
}: {
  assetID: number
  currentUserId: number | null
}) {
  const toast = useToast()
  const [reviews, setReviews] = useState<Review[] | null>(null)

  const load = useCallback(() => {
    let cancelled = false
    setReviews(null)
    api
      .get<Review[]>(`/api/v1/assets/${assetID}/reviews`)
      .then((data) => {
        if (!cancelled) setReviews(data)
      })
      .catch(() => {
        if (!cancelled) setReviews([])
      })
    return () => {
      cancelled = true
    }
  }, [assetID])

  useEffect(() => {
    const cancel = load()
    return cancel
  }, [load])

  const myReview = useMemo<Review | null>(() => {
    if (!reviews || currentUserId === null) return null
    return reviews.find((r) => r.user_id === currentUserId) ?? null
  }, [reviews, currentUserId])

  return (
    <section className="bg-parchment border-4 border-ink shadow-pixel">
      <h2 className="bg-arcane text-parchment font-pixel text-xs uppercase border-b-4 border-ink px-4 py-3">
        ▶ Avaliações
      </h2>
      <div className="p-4 space-y-4">
        {/* Form: só pra usuário logado. Backend rejeita se não tiver
            comprado — toast cobre. */}
        {currentUserId !== null && (
          <ReviewForm
            assetID={assetID}
            existing={myReview}
            onSaved={(saved) => {
              // Optimistic-ish: atualiza a lista local trocando o item
              // do user pelo retornado, ou prepending se for novo.
              setReviews((prev) => {
                if (!prev) return prev
                const without = prev.filter((r) => r.id !== saved.id)
                return [saved, ...without]
              })
            }}
            onDeleted={(id) => {
              setReviews((prev) => prev?.filter((r) => r.id !== id) ?? prev)
            }}
            toast={toast}
          />
        )}

        {/* Lista */}
        {reviews === null ? (
          <div className="text-xs uppercase tracking-widest animate-pulse">
            ▌ Carregando avaliações...
          </div>
        ) : reviews.length === 0 ? (
          <p className="text-xs text-ink/60 tracking-wider">
            Nenhuma avaliação ainda — seja o primeiro a avaliar.
          </p>
        ) : (
          <ul className="space-y-3">
            {reviews.map((r) => (
              <ReviewItem key={r.id} review={r} />
            ))}
          </ul>
        )}
      </div>
    </section>
  )
}

// ReviewItem: 1 linha de review com avatar, nome, estrelas, data e
// comentário. Não mostra ações de editar/deletar — essas ficam só no
// form do próprio usuário (visualmente separadas).
function ReviewItem({ review }: { review: Review }) {
  return (
    <li className="border-2 border-ink/20 px-3 py-3 space-y-2">
      <div className="flex items-center gap-2 flex-wrap">
        <Avatar
          avatarPath={review.author_avatar_path}
          name={review.author_display_name ?? '?'}
          size="xs"
        />
        {review.author_username ? (
          <Link
            to={`/u/${review.author_username}`}
            className="text-xs font-bold uppercase tracking-wider hover:text-arcane hover:underline underline-offset-4 decoration-2"
          >
            {review.author_display_name ?? review.author_username}
          </Link>
        ) : (
          <span className="text-xs font-bold uppercase tracking-wider">
            {review.author_display_name ?? 'anônimo'}
          </span>
        )}
        <StarRating value={review.rating} size="sm" />
        <span className="text-[10px] text-ink/60 tracking-wider ml-auto">
          {formatDate(review.created_at)}
        </span>
      </div>
      {review.comment.trim() && (
        <p className="text-sm leading-relaxed whitespace-pre-wrap">
          {review.comment}
        </p>
      )}
    </li>
  )
}

// ReviewForm: formulário de criar/editar review do próprio usuário.
// Modo determinado por `existing`:
//   - null: form vazio com botão "Avaliar"
//   - Review: form pré-populado com botão "Atualizar" + "Excluir"
function ReviewForm({
  assetID,
  existing,
  onSaved,
  onDeleted,
  toast,
}: {
  assetID: number
  existing: Review | null
  onSaved: (saved: Review) => void
  onDeleted: (id: number) => void
  toast: ReturnType<typeof useToast>
}) {
  const [rating, setRating] = useState(existing?.rating ?? 0)
  const [comment, setComment] = useState(existing?.comment ?? '')
  const [submitting, setSubmitting] = useState(false)

  // Sync quando o existing muda (vem da lista após fetch ou
  // após onSaved).
  useEffect(() => {
    setRating(existing?.rating ?? 0)
    setComment(existing?.comment ?? '')
  }, [existing?.id])

  async function handleSubmit() {
    if (rating < 1 || rating > 5) {
      toast.error('Escolha de 1 a 5 estrelas')
      return
    }
    setSubmitting(true)
    try {
      const body = { rating, comment: comment.trim() }
      const saved = existing
        ? await api.put<Review>(`/api/v1/reviews/${existing.id}`, body)
        : await api.post<Review>(`/api/v1/assets/${assetID}/reviews`, body)
      onSaved(saved)
      toast.success(existing ? 'Avaliação atualizada' : 'Avaliação publicada')
    } catch (err) {
      toast.error(messageForReview(err))
    } finally {
      setSubmitting(false)
    }
  }

  async function handleDelete() {
    if (!existing) return
    setSubmitting(true)
    try {
      await api.delete(`/api/v1/reviews/${existing.id}`)
      onDeleted(existing.id)
      toast.success('Avaliação removida')
      setRating(0)
      setComment('')
    } catch (err) {
      toast.error(messageForReview(err))
    } finally {
      setSubmitting(false)
    }
  }

  return (
    <div className="bg-parchment border-2 border-ink shadow-pixel-sm p-3 space-y-3">
      <p className="text-[10px] font-bold uppercase tracking-widest text-ink/70">
        ▸ {existing ? 'Sua avaliação' : 'Deixe sua avaliação'}
      </p>
      <StarRating value={rating} onChange={setRating} size="lg" />
      <textarea
        rows={2}
        maxLength={2000}
        value={comment}
        onChange={(e) => setComment(e.target.value)}
        placeholder="Comentário (opcional)"
        className="
          block w-full bg-white text-ink border-2 border-ink
          px-2 py-1 text-xs font-mono
          focus:outline-none focus:shadow-pixel-sm
        "
      />
      <div className="flex flex-wrap gap-2">
        <button
          type="button"
          onClick={handleSubmit}
          disabled={submitting || rating < 1}
          className="
            bg-arcane text-parchment border-2 border-ink shadow-pixel-sm
            px-3 py-1 text-[10px] font-bold uppercase tracking-widest
            transition-all duration-75 ease-out
            hover:translate-x-[1px] hover:translate-y-[1px] hover:shadow-none
            disabled:opacity-50 disabled:hover:translate-x-0 disabled:hover:translate-y-0 disabled:hover:shadow-pixel-sm
          "
        >
          {submitting ? '...' : existing ? '▶ Atualizar' : '▶ Avaliar'}
        </button>
        {existing && (
          <button
            type="button"
            onClick={handleDelete}
            disabled={submitting}
            className="
              bg-ink text-parchment border-2 border-ink shadow-pixel-sm
              px-3 py-1 text-[10px] font-bold uppercase tracking-widest
              transition-all duration-75 ease-out
              hover:translate-x-[1px] hover:translate-y-[1px] hover:shadow-none
              disabled:opacity-50
            "
          >
            ✗ Remover
          </button>
        )}
      </div>
    </div>
  )
}

// messageForReview: traduz erros conhecidos. 403 sem compra é o caso
// mais comum (UX: usuário tenta avaliar sem ter comprado).
function messageForReview(err: unknown): string {
  if (err instanceof ApiError) {
    if (err.status === 403) return 'É preciso comprar o asset pra avaliar'
    if (err.status === 409) return 'Você já avaliou este asset'
    const body = err.body as { error?: string } | string
    if (typeof body === 'object' && body?.error) return body.error
  }
  return 'Falha ao salvar avaliação'
}

// SimilarSection: busca e renderiza assets similares no fim da página.
//
// Trata 3 estados:
//   - loading: skeletons
//   - vazio: nada (não polui a UI com "sem resultados" — só some)
//   - tem: header + grid de 4 cards
//
// useEffect refaz quando assetID muda — navegação interna pra outro
// asset (clicando num card similar) atualiza esta sessão sem refresh.
function SimilarSection({ assetID }: { assetID: number }) {
  const [similar, setSimilar] = useState<Asset[] | null>(null)

  useEffect(() => {
    let cancelled = false
    setSimilar(null)

    api
      .get<Asset[]>(`/api/v1/assets/${assetID}/similar?limit=4`)
      .then((data) => {
        if (!cancelled) setSimilar(data)
      })
      .catch(() => {
        // Falha silenciosa: similares é discovery, não conteúdo
        // crítico. Setamos [] pra esconder a sessão sem ruído.
        if (!cancelled) setSimilar([])
      })

    return () => {
      cancelled = true
    }
  }, [assetID])

  // Não-found / vazio: não renderiza nada. O detalhe acima já é
  // self-contained.
  if (similar !== null && similar.length === 0) return null

  return (
    <section className="bg-parchment border-4 border-ink shadow-pixel">
      <h2 className="bg-arcane text-parchment font-pixel text-xs uppercase border-b-4 border-ink px-4 py-3">
        ▶ Você também pode gostar
      </h2>
      <div className="p-4">
        <div className="grid grid-cols-1 sm:grid-cols-2 md:grid-cols-3 lg:grid-cols-4 gap-4">
          {similar === null
            ? Array.from({ length: 4 }).map((_, i) => (
                <AssetCardSkeleton key={i} />
              ))
            : similar.map((a) => <AssetCard key={a.id} asset={a} />)}
        </div>
      </div>
    </section>
  )
}

// OwnerPanel: ações destrutivas isoladas num card próprio, fora do
// painel de Info. Exclusão tem confirm inline (estado local) em vez
// de window.confirm() — preserva a estética pixel-art e não quebra
// o usuário com um dialog nativo do navegador.
function OwnerPanel({
  asset,
  onConfirmDelete,
}: {
  asset: Asset
  onConfirmDelete: () => void | Promise<void>
}) {
  const [confirming, setConfirming] = useState(false)
  const [deleting, setDeleting] = useState(false)

  async function handleConfirm() {
    setDeleting(true)
    try {
      await onConfirmDelete()
      // Sem reset de state — em caso de sucesso a página será
      // desmontada via navigate('/').
    } catch {
      setDeleting(false)
      setConfirming(false)
    }
  }

  return (
    <section>
      <div className="bg-parchment border-4 border-ink shadow-pixel">
        <h2 className="bg-arcane text-parchment font-pixel text-xs uppercase border-b-4 border-ink px-4 py-3">
          ▶ Zona do dono
        </h2>
        <div className="p-6">
          {confirming ? (
            <div className="space-y-4">
              <p className="text-sm">
                <span className="font-bold uppercase tracking-widest">
                  ✗ Excluir?
                </span>{' '}
                Você vai remover permanentemente{' '}
                <span className="italic">“{asset.title}”</span>. Esta
                ação não pode ser desfeita.
              </p>
              <div className="flex flex-wrap gap-3">
                <button
                  type="button"
                  onClick={handleConfirm}
                  disabled={deleting}
                  className="
                    bg-ink text-parchment border-4 border-ink shadow-pixel
                    px-4 py-2 text-xs font-bold uppercase tracking-widest
                    transition-all duration-75 ease-out
                    hover:translate-x-[2px] hover:translate-y-[2px] hover:shadow-none
                    disabled:opacity-50 disabled:hover:translate-x-0 disabled:hover:translate-y-0 disabled:hover:shadow-pixel
                  "
                >
                  {deleting ? '...' : '✗ Sim, excluir'}
                </button>
                <button
                  type="button"
                  onClick={() => setConfirming(false)}
                  disabled={deleting}
                  className="
                    bg-parchment text-ink border-4 border-ink shadow-pixel
                    px-4 py-2 text-xs font-bold uppercase tracking-widest
                    transition-all duration-75 ease-out
                    hover:translate-x-[2px] hover:translate-y-[2px] hover:shadow-none
                    disabled:opacity-50
                  "
                >
                  ◀ Cancelar
                </button>
              </div>
            </div>
          ) : (
            <div className="flex flex-wrap items-center gap-3">
              <Link
                to={`/asset/${asset.id}/edit`}
                className="
                  bg-arcane text-parchment border-4 border-ink shadow-pixel
                  px-4 py-2 text-xs font-bold uppercase tracking-widest
                  transition-all duration-75 ease-out
                  hover:translate-x-[2px] hover:translate-y-[2px] hover:shadow-none
                "
              >
                ▶ Editar
              </Link>
              <button
                type="button"
                onClick={() => setConfirming(true)}
                className="
                  bg-parchment text-ink border-4 border-ink shadow-pixel
                  px-4 py-2 text-xs font-bold uppercase tracking-widest
                  transition-all duration-75 ease-out
                  hover:translate-x-[2px] hover:translate-y-[2px] hover:shadow-none
                "
              >
                ✗ Excluir
              </button>
            </div>
          )}
        </div>
      </div>
    </section>
  )
}

function LoadingState() {
  return (
    <div className="max-w-md mx-auto mt-16 p-6">
      <div className="bg-parchment border-4 border-ink shadow-pixel p-8 text-center">
        <p className="text-4xl mb-4 animate-pulse" aria-hidden="true">
          ▌
        </p>
        <p className="text-sm font-bold uppercase tracking-widest">
          Carregando dados do projeto...
        </p>
      </div>
    </div>
  )
}

function NotFoundState() {
  return (
    <div className="max-w-md mx-auto mt-16 p-6">
      <div className="bg-parchment border-4 border-ink shadow-pixel p-8 text-center">
        <p className="text-4xl mb-4" aria-hidden="true">
          ✗
        </p>
        <p className="text-sm font-bold uppercase tracking-widest mb-2">
          Asset não encontrado
        </p>
        <p className="text-xs text-ink/70 tracking-wider mb-6">
          O ID solicitado não existe no catálogo.
        </p>
        <Link
          to="/"
          className="
            inline-block bg-arcane text-parchment border-4 border-ink shadow-pixel
            px-4 py-2 text-xs font-bold uppercase tracking-widest
            transition-all duration-75 ease-out
            hover:translate-x-[2px] hover:translate-y-[2px] hover:shadow-none
          "
        >
          ◀ Voltar à galeria
        </Link>
      </div>
    </div>
  )
}

function ErrorState({ message }: { message: string }) {
  return (
    <div className="max-w-md mx-auto mt-16 p-6">
      <div className="bg-ink text-parchment border-4 border-ink shadow-pixel p-8 text-center">
        <p className="text-4xl mb-4" aria-hidden="true">
          ✗
        </p>
        <p className="text-sm font-bold uppercase tracking-widest mb-6">
          {message}
        </p>
        <Link
          to="/"
          className="
            inline-block bg-parchment text-ink border-4 border-ink shadow-pixel
            px-4 py-2 text-xs font-bold uppercase tracking-widest
            transition-all duration-75 ease-out
            hover:translate-x-[2px] hover:translate-y-[2px] hover:shadow-none
          "
        >
          ◀ Galeria
        </Link>
      </div>
    </div>
  )
}

function messageForDelete(err: unknown): string {
  if (err instanceof ApiError) {
    if (err.status === 401) return 'Sessão expirada'
    if (err.status === 403) return 'Este asset não é seu'
    if (err.status === 404) return 'Asset já foi removido'
  }
  return 'Falha ao excluir o asset'
}


// AssetPacksSection: badges/links pros packs que CONTÊM este asset.
// Discreta — quando o asset não está em nenhum pack, sumimos sem
// nem renderizar header (evita slot vazio na página).
//
// Fetch isolado: a falha (rede, 5xx) deixa a sessão silenciosa em
// vez de quebrar o fluxo do AssetDetail. Loading também é silencioso
// porque é um nice-to-have.
function AssetPacksSection({ assetID }: { assetID: number }) {
  const [packs, setPacks] = useState<Pack[] | null>(null)

  useEffect(() => {
    let cancelled = false
    api
      .get<Pack[]>(`/api/v1/assets/${assetID}/packs`)
      .then((data) => {
        if (!cancelled) setPacks(data)
      })
      .catch(() => {
        if (!cancelled) setPacks([])
      })
    return () => {
      cancelled = true
    }
  }, [assetID])

  if (!packs || packs.length === 0) return null

  return (
    <section className="bg-parchment border-4 border-arcane shadow-pixel">
      <h2 className="bg-arcane text-parchment font-pixel text-xs uppercase border-b-4 border-ink px-4 py-3">
        ◆ Também faz parte de
      </h2>
      <ul className="p-4 space-y-2">
        {packs.map((p) => {
          const count = p.items_count ?? 0
          return (
            <li key={p.id}>
              <Link
                to={`/pack/${p.id}`}
                className="
                  flex items-center justify-between gap-3
                  border-2 border-ink shadow-pixel-sm p-3
                  transition-all duration-75 ease-out
                  hover:translate-x-[2px] hover:translate-y-[2px] hover:shadow-none
                  bg-parchment
                "
              >
                <div className="min-w-0">
                  <p className="text-[10px] uppercase tracking-widest text-arcane font-bold">
                    ◆ Pack · {count} {count === 1 ? 'asset' : 'assets'}
                  </p>
                  <p className="font-bold text-sm uppercase tracking-wider truncate">
                    {p.title}
                  </p>
                </div>
                <p className="text-sm font-bold flex-shrink-0">
                  ✦ {formatPrice(p.price_cents)}
                </p>
              </Link>
            </li>
          )
        })}
      </ul>
    </section>
  )
}
