import { memo, useCallback, useEffect, useMemo, useState } from 'react'
import { Link } from 'react-router-dom'
import { api, fileUrl, type Purchase } from '../api/client'
import { formatDate, formatPrice } from '../lib/format'
import LineSkeleton from '../components/LineSkeleton'

// /library: histórico de compras. Antes era stub "em breve"; agora
// alimenta-se de GET /api/v1/my/library, que devolve Purchase[]
// com snapshot do preço + asset aninhado (nullable se o vendedor
// deletou o asset depois da compra).
//
// Cada item mostra:
//   - thumbnail + título + autor (se asset ainda existe)
//   - preço pago (snapshot, NÃO o preço atual)
//   - data da compra
//   - link "Baixar modelo" → /uploads/models/<uuid>.glb
//   - se asset foi deletado: placeholder "[Asset removido]"
//
// Sem grid: igual carrinho, é uma lista temporal de revisão, não
// uma vitrine de descoberta.

export default function Library() {
  const [purchases, setPurchases] = useState<Purchase[] | null>(null)
  const [error, setError] = useState<string | null>(null)

  const load = useCallback(() => {
    setError(null)
    setPurchases(null)
    let cancelled = false

    api
      .get<Purchase[]>('/api/v1/my/library')
      .then((data) => {
        if (!cancelled) setPurchases(data)
      })
      .catch(() => {
        if (!cancelled) setError('Falha ao carregar sua biblioteca.')
      })

    return () => {
      cancelled = true
    }
  }, [])

  useEffect(() => {
    const cancel = load()
    return cancel
  }, [load])

  return (
    <div className="max-w-3xl mx-auto p-6 space-y-6">
      <Hero
        count={error ? null : purchases?.length ?? null}
        loading={!error && purchases === null}
      />
      <Content purchases={purchases} error={error} onRetry={load} />
    </div>
  )
}

function Hero({
  count,
  loading,
}: {
  count: number | null
  loading: boolean
}) {
  return (
    <header className="bg-parchment border-4 border-ink shadow-pixel">
      <p className="bg-arcane text-parchment font-pixel text-xs uppercase border-b-4 border-ink px-4 py-3">
        ▶ Biblioteca
      </p>
      <div className="px-6 py-5">
        <h1 className="text-xl md:text-2xl font-bold uppercase tracking-wider leading-tight">
          ✓ Baú do Aventureiro
        </h1>
        <p className="text-xs uppercase tracking-widest text-ink/60 mt-1">
          ▸ {subtitle(count, loading)}
        </p>
      </div>
    </header>
  )
}

function subtitle(count: number | null, loading: boolean): string {
  if (loading) return 'Carregando biblioteca...'
  if (count === null) return 'Falha ao carregar'
  if (count === 0) return 'Sem compras ainda'
  if (count === 1) return '1 asset comprado'
  return `${count} assets comprados`
}

function Content({
  purchases,
  error,
  onRetry,
}: {
  purchases: Purchase[] | null
  error: string | null
  onRetry: () => void
}) {
  if (error) {
    return (
      <div className="bg-ink text-parchment border-4 border-ink shadow-pixel p-8 text-center">
        <p className="text-4xl mb-4" aria-hidden="true">
          ✗
        </p>
        <p className="text-sm font-bold uppercase tracking-widest mb-6">
          {error}
        </p>
        <button
          onClick={onRetry}
          className="
            bg-parchment text-ink border-4 border-ink shadow-pixel
            px-4 py-2 text-xs font-bold uppercase tracking-widest
            transition-all duration-75 ease-out
            hover:translate-x-[2px] hover:translate-y-[2px] hover:shadow-none
          "
        >
          ▶ Tentar novamente
        </button>
      </div>
    )
  }

  if (purchases === null) {
    return (
      <ul className="space-y-3" aria-busy="true" aria-live="polite">
        {Array.from({ length: 3 }).map((_, i) => (
          <LineSkeleton key={i} />
        ))}
      </ul>
    )
  }

  if (purchases.length === 0) {
    return (
      <div className="bg-parchment border-4 border-ink shadow-pixel p-12 text-center">
        <p className="text-5xl mb-4" aria-hidden="true">
          ⚿
        </p>
        <p className="text-sm font-bold uppercase tracking-widest mb-2">
          Sem compras ainda
        </p>
        <p className="text-xs text-ink/70 tracking-wider mb-6">
          Explore o catálogo e adicione assets ao carrinho.
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
          ▶ Explorar o catálogo
        </Link>
      </div>
    )
  }

  return <GroupedList purchases={purchases} />
}

// GroupedList: separa em duas seções —
//   1. Pack groups: purchases que têm `from_pack_id` viram um header
//      ("◆ Pack Medieval") seguido das suas N purchases indentadas.
//   2. Direct purchases: assets comprados soltos, sem agrupamento.
//
// Pack órfão (from_pack_id veio mas from_pack_title null = pack
// deletado depois) cai pro grupo "Pack removido" pra explicar o
// agrupamento sem perder a linha.
function GroupedList({ purchases }: { purchases: Purchase[] }) {
  const { groups, direct } = useMemo(() => groupByPack(purchases), [purchases])
  return (
    <div className="space-y-6">
      {groups.map((g) => (
        <section key={g.key} className="space-y-2">
          <header className="bg-arcane text-parchment border-4 border-ink shadow-pixel-sm px-4 py-2 flex items-center justify-between gap-3">
            <div className="min-w-0">
              <p className="text-[10px] uppercase tracking-widest text-parchment/80">
                ◆ Comprado via pack
              </p>
              {g.packID ? (
                <Link
                  to={`/pack/${g.packID}`}
                  className="block font-bold text-sm uppercase tracking-wider truncate hover:underline underline-offset-4 decoration-2"
                  title={g.title}
                >
                  {g.title}
                </Link>
              ) : (
                <p className="font-bold text-sm uppercase tracking-wider truncate italic opacity-80">
                  {g.title}
                </p>
              )}
            </div>
            <p className="text-[10px] uppercase tracking-widest font-bold flex-shrink-0">
              {g.items.length} {g.items.length === 1 ? 'asset' : 'assets'}
            </p>
          </header>
          <ul className="space-y-2 pl-3 border-l-4 border-arcane">
            {g.items.map((p) => (
              <LibraryLine key={p.id} purchase={p} />
            ))}
          </ul>
        </section>
      ))}
      {direct.length > 0 && (
        <ul className="space-y-3">
          {direct.map((p) => (
            <LibraryLine key={p.id} purchase={p} />
          ))}
        </ul>
      )}
    </div>
  )
}

// PackGroup: assets comprados juntos num pack. `packID` null indica
// pack que foi DELETADO depois — mantemos o agrupamento pra que o
// total faça sentido na cabeça do user, mas sem link.
type PackGroup = {
  key: string // dedupe key: "pack-<id>" ou "removed-<index>"
  packID: number | null
  title: string
  items: Purchase[]
}

// groupByPack particiona o array. Usa Map pra deduplicar por
// from_pack_id; purchases sem from_pack_id vão em `direct`.
// Pack órfão (id presente mas title null) aparece como "Pack removido"
// com o id no key pra distinguir múltiplos órfãos.
function groupByPack(purchases: Purchase[]): {
  groups: PackGroup[]
  direct: Purchase[]
} {
  const byPack = new Map<number, PackGroup>()
  const orphans = new Map<number, PackGroup>()
  const direct: Purchase[] = []

  for (const p of purchases) {
    if (p.from_pack_id == null) {
      direct.push(p)
      continue
    }
    if (p.from_pack_title) {
      const existing = byPack.get(p.from_pack_id)
      if (existing) existing.items.push(p)
      else {
        byPack.set(p.from_pack_id, {
          key: `pack-${p.from_pack_id}`,
          packID: p.from_pack_id,
          title: p.from_pack_title,
          items: [p],
        })
      }
    } else {
      // Pack deletado depois — agrupa pelo id original mesmo sem título.
      const existing = orphans.get(p.from_pack_id)
      if (existing) existing.items.push(p)
      else {
        orphans.set(p.from_pack_id, {
          key: `removed-${p.from_pack_id}`,
          packID: null,
          title: '[Pack removido]',
          items: [p],
        })
      }
    }
  }

  return {
    groups: [...byPack.values(), ...orphans.values()],
    direct,
  }
}

// LibraryLine: linha de uma compra. Trata o caso de asset deletado
// (purchase.asset === null) renderizando placeholder.
//
// memo: `purchase` é estável por compra; retry/loading do pai não
// cascateia.
const LibraryLine = memo(function LibraryLine({ purchase }: { purchase: Purchase }) {
  const { asset } = purchase

  if (!asset) {
    return (
      <li className="bg-parchment border-4 border-ink shadow-pixel p-3 flex items-center gap-3 opacity-60">
        <div className="w-16 h-16 bg-ink/20 border-2 border-ink shadow-pixel-sm flex items-center justify-center text-ink/50 text-xs">
          ✗
        </div>
        <div className="flex-1 min-w-0">
          <p className="font-bold text-sm uppercase tracking-wider">
            [Asset removido]
          </p>
          <p className="text-[10px] text-ink/60 mt-1">
            O vendedor removeu este asset do catálogo.
          </p>
        </div>
        <div className="text-right">
          <p className="text-sm font-bold">
            ✦ {formatPrice(purchase.price_cents_snapshot)}
          </p>
          <p className="text-[10px] text-ink/60 mt-1">
            {formatDate(purchase.purchased_at)}
          </p>
        </div>
      </li>
    )
  }

  return (
    <li className="bg-parchment border-4 border-ink shadow-pixel p-3 flex items-center gap-3">
      <Link to={`/asset/${asset.id}`} className="flex-shrink-0">
        <img
          src={fileUrl(asset.thumbnail_path)}
          alt={asset.title}
          className="w-16 h-16 object-cover border-2 border-ink shadow-pixel-sm"
        />
      </Link>
      <div className="flex-1 min-w-0">
        <Link
          to={`/asset/${asset.id}`}
          className="block font-bold text-sm uppercase tracking-wider truncate hover:text-arcane"
          title={asset.title}
        >
          {asset.title}
        </Link>
        <p className="text-xs text-ink/70 tracking-wider truncate">
          por{' '}
          <span className="font-bold">
            {asset.author_name ?? 'anônimo'}
          </span>
        </p>
        <a
          // Download direto: o backend serve /uploads/models/*.glb
          // estaticamente. `download` força o navegador a salvar em
          // vez de tentar renderizar (browsers não sabem renderizar
          // .glb nativamente, mas pra .gltf JSON poderiam).
          href={fileUrl(asset.model_path)}
          download
          className="inline-block mt-1 text-[10px] uppercase tracking-widest font-bold underline underline-offset-4 decoration-2 hover:text-arcane"
        >
          ⬇ Baixar modelo
        </a>
      </div>
      <div className="text-right flex-shrink-0">
        <p className="text-sm font-bold">
          ✦ {formatPrice(purchase.price_cents_snapshot)}
        </p>
        <p className="text-[10px] text-ink/60 mt-1">
          {formatDate(purchase.purchased_at)}
        </p>
      </div>
    </li>
  )
})

