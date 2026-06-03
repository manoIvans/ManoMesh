import { useCallback, useEffect, useMemo, useState } from 'react'
import { useNavigate, useParams } from 'react-router-dom'
import {
  ApiError,
  api,
  fileUrl,
  type Asset,
  type Pack,
} from '../api/client'
import { useAuth } from '../auth/AuthContext'
import { fromCents, toCents } from '../lib/money'
import { useToast } from '../components/Toast'

// /dashboard/packs/:id/edit — form de edição de pack existente.
// Espelha PackNew mas pré-popula com dados do pack via GET /packs/:id.
// PUT JSON (sem multipart) — troca de capa é endpoint separado
// (futuro: botão "trocar capa" aqui). Por enquanto, sem upload no edit.
//
// Validação:
//   - Ownership: se /packs/:id devolve owner_id != currentUserId,
//     navega pro detalhe público em vez de mostrar form.
//   - Min 2 / max 50 assets: mesma regra do Create.
//
// Em sucesso, navega pro /pack/:id atualizado.

const MIN_ITEMS = 2
const MAX_ITEMS = 50

export default function PackEdit() {
  const { id } = useParams<{ id: string }>()
  const navigate = useNavigate()
  const toast = useToast()
  const { currentUserId } = useAuth()

  const [pack, setPack] = useState<Pack | null>(null)
  const [assets, setAssets] = useState<Asset[] | null>(null)
  const [loadError, setLoadError] = useState<string | null>(null)

  const [title, setTitle] = useState('')
  const [description, setDescription] = useState('')
  const [priceInput, setPriceInput] = useState('')
  const [selected, setSelected] = useState<Set<number>>(new Set())
  const [saving, setSaving] = useState(false)

  const load = useCallback(() => {
    if (!id) return () => {}
    setLoadError(null)
    setPack(null)
    setAssets(null)
    let cancelled = false

    Promise.all([
      api.get<Pack>(`/api/v1/packs/${id}`),
      api.get<Asset[]>('/api/v1/my/assets'),
    ])
      .then(([p, a]) => {
        if (cancelled) return
        setPack(p)
        setAssets(a)
        setTitle(p.title)
        setDescription(p.description)
        setPriceInput(fromCents(p.price_cents))
        setSelected(new Set((p.items ?? []).map((it) => it.id)))
      })
      .catch((err) => {
        if (cancelled) return
        if (err instanceof ApiError && err.status === 404) {
          setLoadError('Pack não encontrado.')
        } else {
          setLoadError('Falha ao carregar pack.')
        }
      })

    return () => {
      cancelled = true
    }
  }, [id])

  useEffect(() => {
    const cancel = load()
    return cancel
  }, [load])

  // Ownership guard: se o pack carregou mas não é do user logado,
  // redireciona pro detalhe público (não bloqueia, só não mostra form).
  useEffect(() => {
    if (pack && currentUserId !== null && pack.owner_id !== currentUserId) {
      navigate(`/pack/${pack.id}`, { replace: true })
    }
  }, [pack, currentUserId, navigate])

  const priceCents = useMemo(() => toCents(priceInput), [priceInput])

  function toggle(assetID: number) {
    setSelected((prev) => {
      const next = new Set(prev)
      if (next.has(assetID)) next.delete(assetID)
      else next.add(assetID)
      return next
    })
  }

  const canSubmit =
    pack !== null &&
    title.trim().length > 0 &&
    selected.size >= MIN_ITEMS &&
    selected.size <= MAX_ITEMS &&
    priceCents !== null &&
    priceCents >= 0 &&
    !saving

  async function handleSubmit(e: React.FormEvent) {
    e.preventDefault()
    if (!canSubmit || !pack) return
    setSaving(true)
    try {
      await api.put<Pack>(`/api/v1/packs/${pack.id}`, {
        title: title.trim(),
        description: description.trim(),
        price_cents: priceCents,
        asset_ids: Array.from(selected),
      })
      toast.success('Pack atualizado')
      navigate(`/pack/${pack.id}`, { replace: true })
    } catch (err) {
      toast.error(messageForUpdate(err))
      setSaving(false)
    }
  }

  if (loadError) {
    return (
      <div className="max-w-3xl mx-auto p-6">
        <div className="bg-ink text-parchment border-4 border-ink shadow-pixel p-8 text-center">
          <p className="text-sm font-bold uppercase tracking-widest mb-6">
            {loadError}
          </p>
          <button
            type="button"
            onClick={() => navigate('/my-store', { replace: true })}
            className="bg-parchment text-ink border-4 border-ink shadow-pixel px-4 py-2 text-xs font-bold uppercase tracking-widest"
          >
            ▶ Voltar à loja
          </button>
        </div>
      </div>
    )
  }

  if (!pack || !assets) {
    return (
      <div className="max-w-4xl mx-auto p-6">
        <div className="bg-parchment border-4 border-ink shadow-pixel p-8 text-center animate-pulse">
          <p className="text-sm font-bold uppercase tracking-widest">
            ▌ Carregando pack...
          </p>
        </div>
      </div>
    )
  }

  return (
    <div className="max-w-4xl mx-auto p-6 space-y-6">
      <header className="bg-parchment border-4 border-ink shadow-pixel">
        <p className="bg-arcane text-parchment font-pixel text-xs uppercase border-b-4 border-ink px-4 py-3">
          ▶ Editar pack
        </p>
        <div className="px-6 py-5">
          <h1 className="text-xl md:text-2xl font-bold uppercase tracking-wider leading-tight">
            {pack.title}
          </h1>
          <p className="text-xs uppercase tracking-widest text-ink/60 mt-1">
            ▸ Edite metadados e items. Pra trocar a capa, use o botão na
            página do pack.
          </p>
        </div>
      </header>

      <form onSubmit={handleSubmit} className="space-y-5">
        <div className="bg-parchment border-4 border-ink shadow-pixel p-5 space-y-4">
          <label className="block space-y-1">
            <span className="text-xs uppercase tracking-widest font-bold text-ink/70">
              Título *
            </span>
            <input
              type="text"
              value={title}
              onChange={(e) => setTitle(e.target.value)}
              maxLength={200}
              required
              className="w-full bg-parchment border-2 border-ink px-3 py-2 text-sm focus:outline-none focus:bg-arcane/10"
            />
          </label>
          <label className="block space-y-1">
            <span className="text-xs uppercase tracking-widest font-bold text-ink/70">
              Descrição
            </span>
            <textarea
              value={description}
              onChange={(e) => setDescription(e.target.value)}
              maxLength={2000}
              rows={3}
              className="w-full bg-parchment border-2 border-ink px-3 py-2 text-sm focus:outline-none focus:bg-arcane/10"
            />
          </label>
          <label className="block space-y-1">
            <span className="text-xs uppercase tracking-widest font-bold text-ink/70">
              Preço (R$) *
            </span>
            <input
              type="text"
              inputMode="decimal"
              value={priceInput}
              onChange={(e) => setPriceInput(e.target.value)}
              required
              className="w-full bg-parchment border-2 border-ink px-3 py-2 text-sm focus:outline-none focus:bg-arcane/10"
            />
          </label>
        </div>

        <fieldset className="bg-parchment border-4 border-ink shadow-pixel p-5">
          <legend className="text-xs uppercase tracking-widest font-bold text-ink/70 px-2">
            ▸ Selecione 2-{MAX_ITEMS} assets
          </legend>
          {assets.length === 0 ? (
            <p className="text-xs text-ink/60 mt-3">
              Você não tem assets publicados — edição não vai funcionar
              sem pelo menos 2.
            </p>
          ) : (
            <div className="grid grid-cols-2 sm:grid-cols-3 md:grid-cols-4 gap-3 mt-3">
              {assets.map((a) => (
                <button
                  key={a.id}
                  type="button"
                  onClick={() => toggle(a.id)}
                  aria-pressed={selected.has(a.id)}
                  className={`
                    text-left border-4 border-ink shadow-pixel-sm p-2 transition-all duration-75
                    ${
                      selected.has(a.id)
                        ? 'bg-arcane/30 ring-4 ring-arcane'
                        : 'bg-parchment hover:translate-x-[1px] hover:translate-y-[1px] hover:shadow-none'
                    }
                  `}
                >
                  <img
                    src={fileUrl(a.thumbnail_path)}
                    alt={a.title}
                    className="w-full aspect-square object-cover border-2 border-ink"
                    loading="lazy"
                  />
                  <p className="text-[10px] uppercase tracking-widest font-bold truncate mt-2">
                    {a.title}
                  </p>
                </button>
              ))}
            </div>
          )}
        </fieldset>

        <div className="bg-twilight text-parchment border-4 border-ink shadow-pixel p-5 flex flex-wrap items-center justify-between gap-3">
          <p className="text-xs uppercase tracking-widest font-bold">
            {selected.size} / {MAX_ITEMS} selecionado(s)
          </p>
          <button
            type="submit"
            disabled={!canSubmit}
            className="
              bg-parchment text-ink border-4 border-ink shadow-pixel
              px-4 py-3 text-sm font-bold uppercase tracking-widest
              transition-all duration-75 ease-out
              hover:translate-x-[2px] hover:translate-y-[2px] hover:shadow-none
              disabled:opacity-50 disabled:hover:translate-x-0 disabled:hover:translate-y-0 disabled:hover:shadow-pixel
            "
          >
            {saving ? '...' : '▶ Salvar'}
          </button>
        </div>
      </form>
    </div>
  )
}

function messageForUpdate(err: unknown): string {
  if (err instanceof ApiError) {
    if (err.status === 400) {
      const body = err.body as { error?: string } | string
      if (typeof body === 'object' && body?.error) return body.error
      return 'Dados inválidos'
    }
    if (err.status === 403) return 'Este pack não é seu'
    if (err.status === 404) return 'Pack não encontrado'
  }
  return 'Falha ao salvar pack'
}
