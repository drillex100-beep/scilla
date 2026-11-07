import { Link } from "@tanstack/react-router";
import { useQuery } from "@tanstack/react-query";
import { ShieldCheck } from "lucide-react";
import { useMemo, useState } from "react";
import { api, errorText, unwrap, type User } from "../../api/client";
import { onePeriod, useInbounds, useNode, useNodes, useNodeShares, useOverview, userActions, useServerTraffic, useUserMutation, useUsers } from "../../api/hooks";
import { buckets, TrafficChart, type Range } from "../../components/chart";
import { QueryBoundary, StaleNotice } from "../../components/query";
import { useToast } from "../../components/toast";
import { Avatar, Bar, Button, ErrorState, Pill, Segmented, Skeleton, StatePill } from "../../components/ui";
import { getLocale, t } from "../../i18n";
import { bits, bytes, dateShort, expiryText, maskedAs, num, time, uptime } from "../../lib/format";
import { nodeLabel } from "../../lib/node-label";
import { CardHead } from "../../components/layout";

/** The widget kinds the builder offers; the API accepts the same list (plus a few reserved). */
export type WidgetType = "stat" | "traffic" | "shares" | "server" | "attention" | "top" | "payments" | "audit";

export type WidgetConfig = {
  /** stat: which counter of the overview to show. */
  metric?: "online" | "active" | "traffic" | "expiring";
  /** traffic/shares: the period the chart opens with. */
  range?: "24h" | "7d" | "30d";
};

export type WidgetInstance = {
  id: string;
  type: WidgetType;
  x: number;
  y: number;
  w: number;
  h: number;
  config?: WidgetConfig;
};

export const GRID_COLS = 12;
export const ROW_H = 80;
export const ROW_GAP = 16;

export const STAT_METRICS = ["online", "active", "traffic", "expiring"] as const;
export type StatMetric = (typeof STAT_METRICS)[number];

export function defaultMetric(config?: WidgetConfig): StatMetric {
  return config?.metric && (STAT_METRICS as readonly string[]).includes(config.metric) ? config.metric : "online";
}

function Kpi({ label, value, foot }: { label: string; value: React.ReactNode; foot: React.ReactNode }) {
  return (
    <section className="card glass kpi flex h-full flex-col">
      <div className="kpi-label">{label}</div>
      <div className="kpi-value num">{value}</div>
      <div className="kpi-foot">{foot}</div>
    </section>
  );
}

function CardError({ error, onRetry }: { error: unknown; onRetry: () => void }) {
  return <ErrorState text={errorText(error)} onRetry={onRetry} />;
}

export function StatWidget({ config }: { config?: WidgetConfig }) {
  const metric = defaultMetric(config);
  const o = useOverview();
  const d = o.data;
  if (!d) {
    if (o.isError) {
      return (
        <section className="card glass h-full">
          <CardError error={o.error} onRetry={() => void o.refetch()} />
        </section>
      );
    }
    return (
      <section className="card glass kpi h-full" role="status" aria-busy aria-label={t("common.loading")}>
        <Skeleton style={{ width: "50%" }} />
        <Skeleton style={{ width: "40%", height: 28, marginTop: 8 }} />
      </section>
    );
  }
  const delta = d.traffic_yesterday > 0 ? Math.round(((d.traffic_today - d.traffic_yesterday) / d.traffic_yesterday) * 100) : null;
  const [value, unit] = bytes(d.traffic_today).split(" ");
  switch (metric) {
    case "active":
      return (
        <Kpi label={t("dashboard.active")} value={num(d.users_active)} foot={<span>{t("dashboard.activeFoot", { total: num(d.users_total) })}</span>} />
      );
    case "traffic":
      return (
        <Kpi
          label={t("dashboard.trafficToday")}
          value={
            <>
              {value}
              <small>{unit}</small>
            </>
          }
          foot={
            delta === null ? (
              <span>{t("dashboard.noTrafficYesterday")}</span>
            ) : (
              <span className={delta >= 0 ? "text-[var(--leaf-700)]" : ""}>{delta >= 0 ? t("dashboard.moreThanYesterday", { pct: delta }) : t("dashboard.lessThanYesterday", { pct: -delta })}</span>
            )
          }
        />
      );
    case "expiring":
      return (
        <Kpi
          label={t("dashboard.expiring7d")}
          value={num(d.expiring_7d)}
          foot={
            d.expiring_7d > 0 ? (
              <Link to="/users" search={{ state: "expiring", q: "", hidden: "show" }} className="link-btn">
                {t("dashboard.showList")}
              </Link>
            ) : (
              <span>{t("dashboard.noneExpiring")}</span>
            )
          }
        />
      );
    default:
      return (
        <Kpi label={t("dashboard.online")} value={num(d.online)} foot={<span>{t("dashboard.onlineFoot")}</span>} />
      );
  }
}

export function TrafficWidget({ config }: { config?: WidgetConfig } = {}) {
  const [range, setRange] = useState<Range>(config?.range ?? "24h");
  const traffic = useServerTraffic(range);
  const stale = traffic.isPlaceholderData;
  const points = useMemo(() => buckets(traffic.data?.points ?? [], range), [traffic.data, range]);
  const totalDown = points.reduce((a, p) => a + p.down, 0);
  const totalUp = points.reduce((a, p) => a + p.up, 0);
  return (
    <section className="card glass flex h-full flex-col overflow-hidden" aria-labelledby="traffic-title">
      <div className="card-head">
        <div>
          <h2 className="card-title" id="traffic-title">
            {t("dashboard.traffic")}
          </h2>
          <div className="mt-1.5 flex gap-4 text-[13px] text-[var(--ink-600)]">
            <span className="inline-flex items-center gap-2">
              <i className="h-2 w-2 rounded-full bg-[var(--scilla-500)]" /> {t("chart.down")}
            </span>
            <span className="inline-flex items-center gap-2">
              <i className="h-2 w-2 rounded-full bg-[var(--lagoon-500)]" /> {t("chart.up")}
            </span>
          </div>
        </div>
        <Segmented
          label={t("dashboard.period")}
          value={range}
          onChange={setRange}
          options={[
            { value: "24h", label: t("dashboard.range24h") },
            { value: "7d", label: t("dashboard.range7d") },
            { value: "30d", label: t("dashboard.range30d") },
          ]}
        />
      </div>
      <div className="min-h-0 flex-1 overflow-y-auto">
        <QueryBoundary query={traffic} pending={<Skeleton style={{ height: 220, borderRadius: 16 }} />}>
          {() => (
            <>
              <TrafficChart points={points} range={range} stale={stale} />
              <div className="mt-3 flex min-h-5 flex-wrap items-center gap-x-6 gap-y-2 text-[13px] text-[var(--ink-500)]" aria-busy={stale}>
                {stale ? (
                  <>
                    <Skeleton style={{ width: 112, height: 14 }} />
                    <Skeleton style={{ width: 112, height: 14 }} />
                  </>
                ) : (
                  <>
                    <span>
                      {t("chart.down")} <b className="num font-semibold text-[var(--ink-900)]">{bytes(totalDown)}</b>
                    </span>
                    <span>
                      {t("chart.up")} <b className="num font-semibold text-[var(--ink-900)]">{bytes(totalUp)}</b>
                    </span>
                  </>
                )}
              </div>
            </>
          )}
        </QueryBoundary>
      </div>
    </section>
  );
}

export function ServerWidget() {
  const node = useNode();
  const inbounds = useInbounds();
  const [allInbounds, setAllInbounds] = useState(false);
  const n = node.data;
  const list = inbounds.data ?? [];
  const memPct = n && n.system.mem_total ? (n.system.mem_used / n.system.mem_total) * 100 : 0;
  return (
    <section className="card glass flex h-full flex-col overflow-hidden" aria-labelledby="srv-title">
      <CardHead title={t("shell.server")} sub={n?.ok ? `${n.core}${n.started_at ? ` · ${t("dashboard.uptime", { uptime: uptime(n.started_at) })}` : ""}` : t("dashboard.nodeState")} aside={n ? (n.ok ? <Pill tone="ok">{t("shell.running")}</Pill> : <Pill tone="bad">{t("shell.offline")}</Pill>) : null} id="srv-title" />
      {n && !n.ok ? (
        <div className="banner err mb-4" role="alert">
          <span>
            {t("dashboard.nodeUnreachable")} <span className="mono whitespace-nowrap">scilla logs node</span>
          </span>
        </div>
      ) : null}
      {node.isError ? (
        n ? (
          <StaleNotice onRetry={() => void node.refetch()} retrying={node.isFetching} />
        ) : (
          <CardError error={node.error} onRetry={() => void node.refetch()} />
        )
      ) : null}
      <div className="mb-4 grid grid-cols-3 gap-2">
        <div className="metric">
          <div className="metric-label">CPU</div>
          <div className="metric-value num">
            {n ? Math.round(n.system.cpu_percent) : "—"}
            <small>%</small>
          </div>
          <Bar pct={n?.system.cpu_percent ?? 0} className="mt-2" label="CPU" />
        </div>
        <div className="metric">
          <div className="metric-label">{t("dashboard.memory")}</div>
          <div className="metric-value num">
            {n ? Math.round(memPct) : "—"}
            <small>%</small>
          </div>
          <Bar pct={memPct} className="mt-2" label={t("dashboard.memory")} />
        </div>
        <div className="metric">
          <div className="metric-label">{t("dashboard.netDown")}</div>
          <div className="metric-value num text-[15px]">{n ? bits(n.system.net_rx_bps) : "—"}</div>
        </div>
      </div>
      {inbounds.isError && !inbounds.data ? <CardError error={inbounds.error} onRetry={() => void inbounds.refetch()} /> : null}
      <div className="row-list min-h-0 flex-1 overflow-y-auto border-t border-[var(--hairline)] [scrollbar-width:thin]" aria-busy={inbounds.isPending}>
        {inbounds.isPending
          ? [0, 1, 2].map((i) => (
              <div key={i} className="grid grid-cols-[minmax(0,1fr)_auto] items-center gap-3 py-3" aria-hidden>
                <div className="min-w-0">
                  <Skeleton style={{ width: 120 }} />
                  <Skeleton className="mt-1.5" style={{ width: 180 }} />
                </div>
                <Skeleton style={{ width: 48, height: 20, borderRadius: 999 }} />
              </div>
            ))
          : null}
        {inbounds.data && list.length === 0 ? <p className="py-3 text-[13px] text-[var(--ink-500)]">{t("inbounds.emptyTitle")}</p> : null}
        {list.map((l, i) => (
          <div key={l.id} className={`grid grid-cols-[minmax(0,1fr)_auto] items-center gap-3 py-3${i >= 5 && !allInbounds ? " max-xl:hidden" : ""}`}>
            <div className="min-w-0">
              <div className="text-[13px] font-medium">{l.sub_name}</div>
              <div className="truncate text-xs text-[var(--ink-500)]">
                {l.port}/{l.network}
                {l.dest ? ` · ${t("inbounds.maskedAs", { dest: maskedAs(l) })}` : ""}
              </div>
            </div>
            {!l.enabled ? (
              <Pill tone="off">{t("inbounds.off")}</Pill>
            ) : l.status === "error" ? (
              <Pill tone="bad">{t("inbounds.error")}</Pill>
            ) : l.status === "ok" ? (
              <Pill tone="ok">{t("inbounds.ok")}</Pill>
            ) : (
              <Pill tone="off">…</Pill>
            )}
          </div>
        ))}
      </div>
      {list.length > 5 ? (
        <button type="button" className="link-btn mt-1 text-[13px]" aria-expanded={allInbounds} onClick={() => setAllInbounds((v) => !v)}>
          {allInbounds ? t("dashboard.lessInbounds") : t("dashboard.moreInbounds", { n: list.length - 5 })}
        </button>
      ) : null}
      <div className="panel-soft mt-3 flex shrink-0 items-start gap-2 p-3 text-xs leading-4 text-[var(--ink-600)]">
        <ShieldCheck size={16} className="shrink-0 text-[var(--leaf-500)]" aria-hidden />
        <span>{t("dashboard.safetyNote")}</span>
      </div>
    </section>
  );
}

export function SharesWidget({ config }: { config?: WidgetConfig } = {}) {
  const nodes = useNodes();
  const [range, setRange] = useState<Range>(config?.range ?? "24h");
  const several = (nodes.data?.length ?? 0) >= 2;
  const shares = useNodeShares(range, several);
  if (!several) return null;
  const total = shares.data?.total ?? 0;
  return (
    <section className="card glass flex h-full flex-col overflow-hidden" aria-labelledby="node-traffic-title">
      <div className="card-head">
        <div>
          <h2 className="card-title" id="node-traffic-title">
            {t("dashboard.nodeTraffic")}
          </h2>
          <div className="card-sub">{shares.data ? t("dashboard.nodeTrafficSub", { total: bytes(total) }) : "…"}</div>
          {shares.data?.since ? <div className="card-sub">{t("dashboard.nodeTrafficSince", { date: dateShort(shares.data.since) })}</div> : null}
        </div>
        <Segmented
          label={t("dashboard.period")}
          value={range}
          onChange={setRange}
          options={[
            { value: "24h", label: t("dashboard.range24h") },
            { value: "7d", label: t("dashboard.range7d") },
            { value: "30d", label: t("dashboard.range30d") },
          ]}
        />
      </div>
      <div className="min-h-0 flex-1 overflow-y-auto">
        <QueryBoundary
          query={shares}
          pending={
            <div className="space-y-3" role="status" aria-busy aria-label={t("common.loading")}>
              <Skeleton style={{ height: 36 }} />
              <Skeleton style={{ height: 36 }} />
            </div>
          }
        >
          {(data) =>
            total === 0 ? (
              <p className="py-6 text-center text-[13px] text-[var(--ink-500)]">{t("dashboard.nodeTrafficEmpty")}</p>
            ) : (
              <ul className="row-list" aria-busy={shares.isPlaceholderData}>
                {data.items.map((n) => {
                  const pct = (n.bytes / total) * 100;
                  return (
                    <li key={n.id} className="grid grid-cols-[minmax(0,1fr)_auto] items-center gap-x-4 gap-y-2 py-3 sm:grid-cols-[minmax(0,1fr)_minmax(0,2fr)_auto]">
                      <span className="min-w-0">
                        <span className="block truncate text-[13px] font-medium">{nodeLabel(n)}</span>
                        {n.cascade > 0 ? <span className="num block text-xs text-[var(--ink-500)]">{t("dashboard.nodeCascade", { bytes: bytes(n.cascade) })}</span> : null}
                      </span>
                      <span className="num text-right text-[13px] sm:order-3 sm:min-w-[132px]">
                        <b className="font-semibold">{bytes(n.bytes)}</b> <span className="text-[var(--ink-500)]">{percent(pct)}</span>
                      </span>
                      <Bar pct={pct} label={nodeLabel(n)} className="col-span-2 sm:order-2 sm:col-span-1 [&>i]:!bg-[var(--scilla-400)]" />
                    </li>
                  );
                })}
              </ul>
            )
          }
        </QueryBoundary>
      </div>
    </section>
  );
}

function percent(p: number): string {
  return `${p < 10 ? (Math.round(p * 10) / 10).toLocaleString(getLocale()) : Math.round(p)}%`;
}

const ATTENTION_ROWS = 5;

export function AttentionWidget() {
  const attention = useUsers({ state: "attention", q: "" }, { limit: ATTENTION_ROWS, refetchInterval: 30_000 });
  const toast = useToast();
  const extend = useUserMutation(userActions.extend);
  const list: User[] = attention.data?.items ?? [];
  const failed = attention.data === undefined && attention.isError ? attention : undefined;
  const loading = !failed && attention.data === undefined;
  const retry = () => void attention.refetch();
  return (
    <section className="card glass flex h-full flex-col overflow-hidden" aria-labelledby="att-title">
      <CardHead title={t("dashboard.attention")} sub={loading || failed ? "…" : list.length ? t("dashboard.attentionSub") : t("dashboard.allGood")} id="att-title" />
      {failed ? (
        <CardError error={failed.error} onRetry={retry} />
      ) : loading ? (
        <div className="space-y-3" role="status" aria-busy aria-label={t("common.loading")}>
          <Skeleton style={{ height: 36 }} />
          <Skeleton style={{ height: 36 }} />
        </div>
      ) : list.length === 0 ? (
        <p className="flex flex-1 items-center justify-center py-6 text-center text-[13px] text-[var(--ink-500)]">{t("dashboard.attentionEmpty")}</p>
      ) : (
        <div className="row-list min-h-0 flex-1 overflow-y-auto">
          {list.map((u) => {
            const exp = expiryText(u.expires_at);
            return (
              <div key={u.id} className="grid grid-cols-[auto_minmax(0,1fr)_auto] items-center gap-3 py-3">
                <Avatar name={u.name} seed={u.id} />
                <div className="min-w-0">
                  <div className="flex items-center gap-2 text-[13px]">
                    <b className="truncate font-semibold">{u.name}</b>
                    <StatePill state={u.state} />
                  </div>
                  <div className="text-xs text-[var(--ink-500)]">
                    {u.state === "limited" ? t("dashboard.used", { bytes: bytes(u.used_up + u.used_down) }) : u.expires_at ? t("dashboard.until", { date: dateShort(u.expires_at), left: exp.text }) : ""}
                  </div>
                </div>
                {u.state === "limited" ? (
                  <Link to="/users" search={{ state: "all", q: "", user: u.id }} className="btn btn-glass btn-sm">
                    {t("common.open")}
                  </Link>
                ) : (
                  <Button
                    size="sm"
                    loading={extend.isPending && extend.variables?.id === u.id}
                    onClick={() =>
                      extend.mutate(
                        { id: u.id, ...onePeriod(u) },
                        { onSuccess: (r) => toast.ok(t("dashboard.extended", { name: u.name, date: dateShort(r.expires_at!) })), onError: () => toast.error(t("dashboard.extendFailed")) },
                      )
                    }
                  >
                    {u.billing_day != null ? t("dashboard.plusMonth") : t("dashboard.plus30")}
                  </Button>
                )}
              </div>
            );
          })}
        </div>
      )}
    </section>
  );
}

export function TopWidget() {
  const o = useOverview();
  const top = o.data?.top_month ?? [];
  const max = top[0]?.bytes ?? 1;
  return (
    <section className="card glass flex h-full flex-col overflow-hidden" aria-labelledby="top-title">
      <CardHead title={t("dashboard.top")} sub={t("dashboard.topSub")} id="top-title" />
      {o.data === undefined && o.isError ? (
        <CardError error={o.error} onRetry={() => void o.refetch()} />
      ) : o.isPending ? (
        <Skeleton style={{ height: 120 }} />
      ) : top.length === 0 ? (
        <p className="flex flex-1 items-center justify-center py-6 text-center text-[13px] text-[var(--ink-500)]">{t("dashboard.topEmpty")}</p>
      ) : (
        <div className="min-h-0 flex-1 overflow-y-auto">
          {top.map((u, i) => (
            <Link key={u.id} to="/users" search={{ state: "all", q: "", user: u.id }} className="grid grid-cols-[24px_minmax(0,1fr)_auto] items-center gap-3 rounded-xl py-2 hover:bg-[var(--hover)]">
              <span className="font-display text-xs text-[var(--ink-400)]">{i + 1}</span>
              <div className="min-w-0">
                <div className="mb-1.5 truncate text-[13px] font-medium">{u.name}</div>
                <Bar pct={(u.bytes / max) * 100} className="[&>i]:!bg-[var(--scilla-400)]" />
              </div>
              <span className="num min-w-[72px] text-right text-[13px] font-medium">{bytes(u.bytes)}</span>
            </Link>
          ))}
        </div>
      )}
    </section>
  );
}

function paymentMoney(amount: number, currency: string): string {
  if (currency === "XTR") return `${num(amount)} ⭐`;
  return `${(amount / 100).toLocaleString(getLocale(), { minimumFractionDigits: 0, maximumFractionDigits: 2 })} ${currency}`;
}

export function PaymentsWidget() {
  const q = useQuery({ queryKey: ["dash-payments"], queryFn: ({ signal }) => unwrap(api.GET("/api/v1/payments", { params: { query: { limit: 8 } }, signal })), refetchInterval: 60_000 });
  const items = q.data?.items ?? [];
  return (
    <section className="card glass flex h-full flex-col overflow-hidden" aria-labelledby="pay-title">
      <CardHead title={t("dashboard.builder.widgetPayments")} id="pay-title" />
      {q.data === undefined && q.isError ? (
        <CardError error={q.error} onRetry={() => void q.refetch()} />
      ) : q.isPending ? (
        <div className="space-y-3">
          {[0, 1, 2].map((i) => (
            <Skeleton key={i} style={{ height: 28 }} />
          ))}
        </div>
      ) : items.length === 0 ? (
        <p className="flex flex-1 items-center justify-center py-6 text-center text-[13px] text-[var(--ink-500)]">{t("dashboard.builder.paymentsEmpty")}</p>
      ) : (
        <div className="row-list min-h-0 flex-1 overflow-y-auto">
          {items.map((p) => (
            <div key={p.id} className="grid grid-cols-[minmax(0,1fr)_auto] items-center gap-3 py-2.5">
              <div className="min-w-0">
                <div className="truncate text-[13px] font-medium">{p.user_name || p.tg_username || `#${p.tg_id}`}</div>
                <div className="truncate text-xs text-[var(--ink-500)]">
                  {time(p.created_at)} · {p.tariff_name || p.kind}
                </div>
              </div>
              <div className="flex items-center gap-2">
                <span className="num text-[13px] font-medium">{paymentMoney(p.amount, p.currency)}</span>
                {p.status === "paid" || p.status === "applied" ? (
                  <Pill tone="ok">{t("dashboard.builder.paid")}</Pill>
                ) : p.status === "pending" ? (
                  <Pill tone="off">{t("dashboard.builder.pending")}</Pill>
                ) : (
                  <Pill tone="bad">{p.status}</Pill>
                )}
              </div>
            </div>
          ))}
        </div>
      )}
    </section>
  );
}

export function AuditWidget() {
  const q = useQuery({ queryKey: ["dash-audit"], queryFn: ({ signal }) => unwrap(api.GET("/api/v1/audit", { params: { query: { limit: 10 } }, signal })), refetchInterval: 60_000 });
  const items = q.data?.items ?? [];
  return (
    <section className="card glass flex h-full flex-col overflow-hidden" aria-labelledby="audit-title">
      <CardHead title={t("dashboard.builder.widgetAudit")} id="audit-title" />
      {q.data === undefined && q.isError ? (
        <CardError error={q.error} onRetry={() => void q.refetch()} />
      ) : q.isPending ? (
        <div className="space-y-3">
          {[0, 1, 2, 3].map((i) => (
            <Skeleton key={i} style={{ height: 24 }} />
          ))}
        </div>
      ) : items.length === 0 ? (
        <p className="flex flex-1 items-center justify-center py-6 text-center text-[13px] text-[var(--ink-500)]">{t("dashboard.builder.auditEmpty")}</p>
      ) : (
        <div className="row-list min-h-0 flex-1 overflow-y-auto">
          {items.map((a) => (
            <div key={a.id} className="grid grid-cols-[auto_minmax(0,1fr)_auto] items-center gap-3 py-2.5">
              <span className="num text-xs text-[var(--ink-400)]">{time(a.at)}</span>
              <div className="min-w-0">
                <div className="truncate text-[13px] font-medium">{a.action}</div>
                <div className="truncate text-xs text-[var(--ink-500)]">
                  {a.target_type}
                  {a.target_id ? ` · ${a.target_id}` : ""}
                </div>
              </div>
              <span className="max-w-[40%] truncate text-xs text-[var(--ink-500)]">{a.ip}</span>
            </div>
          ))}
        </div>
      )}
    </section>
  );
}

export const WIDGETS: Record<WidgetType, { w: number; h: number; minW: number; minH: number; title: () => string; component: (config?: WidgetConfig) => React.ReactNode }> = {
  stat: { w: 3, h: 2, minW: 3, minH: 2, title: () => t("dashboard.builder.widgetStat"), component: (config) => <StatWidget config={config} /> },
  traffic: { w: 8, h: 5, minW: 6, minH: 4, title: () => t("dashboard.traffic"), component: (config) => <TrafficWidget config={config} /> },
  shares: { w: 4, h: 5, minW: 4, minH: 4, title: () => t("dashboard.nodeTraffic"), component: (config) => <SharesWidget config={config} /> },
  server: { w: 4, h: 6, minW: 4, minH: 4, title: () => t("shell.server"), component: () => <ServerWidget /> },
  attention: { w: 4, h: 5, minW: 4, minH: 4, title: () => t("dashboard.attention"), component: () => <AttentionWidget /> },
  top: { w: 4, h: 4, minW: 3, minH: 3, title: () => t("dashboard.top"), component: () => <TopWidget /> },
  payments: { w: 4, h: 5, minW: 4, minH: 4, title: () => t("dashboard.builder.widgetPayments"), component: () => <PaymentsWidget /> },
  audit: { w: 4, h: 5, minW: 4, minH: 4, title: () => t("dashboard.builder.widgetAudit"), component: () => <AuditWidget /> },
};

export const WIDGET_TYPES = Object.keys(WIDGETS) as WidgetType[];

/** The layout a panel has until the admin changes it: the dashboard as it was. */
export const DEFAULT_LAYOUT: WidgetInstance[] = [
  { id: "stat-online", type: "stat", x: 0, y: 0, w: 3, h: 2, config: { metric: "online" } },
  { id: "stat-active", type: "stat", x: 3, y: 0, w: 3, h: 2, config: { metric: "active" } },
  { id: "stat-traffic", type: "stat", x: 6, y: 0, w: 3, h: 2, config: { metric: "traffic" } },
  { id: "stat-expiring", type: "stat", x: 9, y: 0, w: 3, h: 2, config: { metric: "expiring" } },
  { id: "traffic", type: "traffic", x: 0, y: 2, w: 8, h: 5 },
  { id: "server", type: "server", x: 8, y: 2, w: 4, h: 6 },
  { id: "attention", type: "attention", x: 0, y: 7, w: 4, h: 4 },
  { id: "top", type: "top", x: 4, y: 7, w: 4, h: 4 },
  { id: "shares", type: "shares", x: 8, y: 8, w: 4, h: 4 },
];
