import { Copy, Download, Globe, GripVertical, Plus, RotateCcw, SlidersHorizontal, Trash2, Upload } from "lucide-react";
import { useEffect, useMemo, useRef, useState } from "react";
import { useSaveDashboardLayout } from "../../api/hooks";
import { useDashboardLayout } from "../../api/hooks";
import { Button, Skeleton } from "../../components/ui";
import { useToast } from "../../components/toast";
import { t } from "../../i18n";
import { useMediaQuery } from "../../lib/media";
import { DEFAULT_LAYOUT, GRID_COLS, ROW_GAP, ROW_H, STAT_METRICS, WIDGETS, WIDGET_TYPES, defaultMetric, type StatMetric, type WidgetConfig, type WidgetInstance, type WidgetType } from "./dashboard-widgets";

const MAX_ROWS = 60;
/** Past this many widgets the view mounts the rest only when they scroll near. */
const LAZY_AFTER = 15;

function clamp(v: number, lo: number, hi: number): number {
  return Math.min(hi, Math.max(lo, v));
}

function overlaps(a: WidgetInstance, b: WidgetInstance): boolean {
  return a.x < b.x + b.w && b.x < a.x + a.w && a.y < b.y + b.h && b.y < a.y + a.h;
}

/** Push every widget down until it overlaps nothing before it; keeps the order the admin sees. */
function resolveCollisions(widgets: WidgetInstance[]): WidgetInstance[] {
  const out = [...widgets].sort((a, b) => a.y - b.y || a.x - b.x);
  const placed: WidgetInstance[] = [];
  for (const w of out) {
    let y = w.y;
    while (placed.some((p) => overlaps({ ...w, y }, p))) y++;
    placed.push({ ...w, y: Math.min(y, MAX_ROWS) });
  }
  return placed;
}

function bottomOf(widgets: WidgetInstance[]): number {
  return widgets.reduce((m, w) => Math.max(m, w.y + w.h), 0);
}

function freeId(): string {
  return `w-${Date.now().toString(36)}-${Math.floor(Math.random() * 1e6).toString(36)}`;
}

export function DashboardBuilder() {
  const layout = useDashboardLayout();
  const save = useSaveDashboardLayout();
  const toast = useToast();
  const mobile = useMediaQuery("(max-width: 1023px)");
  const [widgets, setWidgets] = useState<WidgetInstance[] | null>(null);
  const [editing, setEditing] = useState(false);
  const [dirty, setDirty] = useState(false);
  const hydrated = useRef(false);

  useEffect(() => {
    if (hydrated.current || !layout.data) return;
    hydrated.current = true;
    const saved = (layout.data.widgets ?? []).filter((w) => w.type in WIDGETS) as WidgetInstance[];
    const initial = saved.length > 0 ? saved.map((w) => ({ ...w, config: w.config as WidgetInstance["config"] })) : DEFAULT_LAYOUT;
    setWidgets(resolveCollisions(initial));
    setEditing(saved.length === 0);
  }, [layout.data]);

  // Autosave, debounced; only after the admin changed something.
  useEffect(() => {
    if (!dirty || widgets === null) return;
    const id = window.setTimeout(() => {
      save.mutate({ widgets }, {
        onSuccess: () => setDirty(false),
        onError: () => toast.error(t("dashboard.builder.saveFailed")),
      });
    }, 600);
    return () => window.clearTimeout(id);
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [widgets, dirty]);

  const commit = (next: WidgetInstance[]) => {
    setWidgets(resolveCollisions(next));
    setDirty(true);
  };

  const addWidget = (type: WidgetType) => {
    if (widgets === null) return;
    const def = WIDGETS[type];
    const id = freeId();
    let x = 0;
    let y = bottomOf(widgets);
    commit([...widgets, { id, type, x, y, w: def.w, h: def.h }]);
    void x;
  };

  const duplicate = (w: WidgetInstance) => {
    if (widgets === null) return;
    commit([...widgets, { ...w, id: freeId(), x: clamp(w.x + 1, 0, GRID_COLS - w.w), y: w.y + 1 }]);
  };

  const remove = (id: string) => {
    if (widgets === null) return;
    commit(widgets.filter((w) => w.id !== id));
  };

  const reset = () => {
    commit(DEFAULT_LAYOUT.map((w) => ({ ...w })));
  };

  const configure = (id: string, config: WidgetConfig | undefined) => {
    if (widgets === null) return;
    commit(widgets.map((w) => (w.id === id ? { ...w, config } : w)));
  };

  const makeDefault = () => {
    if (widgets === null) return;
    save.mutate(
      { widgets, global: true },
      {
        onSuccess: () => {
          setDirty(false);
          toast.ok(t("dashboard.builder.defaultSaved"));
        },
        onError: () => toast.error(t("dashboard.builder.saveFailed")),
      },
    );
  };

  const exportLayout = () => {
    if (widgets === null) return;
    const blob = new Blob([JSON.stringify({ version: 1, widgets }, null, 2)], { type: "application/json" });
    const url = URL.createObjectURL(blob);
    const a = document.createElement("a");
    a.href = url;
    a.download = "scilla-dashboard.json";
    a.click();
    URL.revokeObjectURL(url);
  };

  const importLayout = (file: File) => {
    file.text().then((text) => {
      try {
        const parsed = JSON.parse(text) as { widgets?: unknown } | unknown[];
        const list = Array.isArray(parsed) ? parsed : (parsed as { widgets?: unknown[] }).widgets;
        if (!Array.isArray(list)) throw new Error("no widgets");
        const clean = (list as Partial<WidgetInstance>[]).filter(
          (w): w is WidgetInstance => typeof w?.id === "string" && typeof w?.type === "string" && w.type in WIDGETS && typeof w?.x === "number" && typeof w?.y === "number" && typeof w?.w === "number" && typeof w?.h === "number",
        ).slice(0, 50);
        if (clean.length === 0) throw new Error("empty");
        commit(clean.map((w, i) => ({ ...w, id: w.id || `w-${i}`, x: clamp(w.x, 0, GRID_COLS - Math.min(w.w, GRID_COLS)), y: clamp(w.y, 0, MAX_ROWS), w: clamp(w.w, WIDGETS[w.type].minW, GRID_COLS), h: clamp(w.h, WIDGETS[w.type].minH, 16) })));
      } catch {
        toast.error(t("dashboard.builder.importInvalid"));
      }
    });
  };

  if (widgets === null) {
    if (layout.isError) {
      return (
        <section className="card glass">
          <p className="p-4 text-[13px] text-[var(--ink-500)]">{t("common.loadFailed")}</p>
          <Button size="sm" onClick={() => void layout.refetch()}>
            {t("common.retry")}
          </Button>
        </section>
      );
    }
    return (
      <div className="grid grid-cols-2 gap-4 xl:grid-cols-4" role="status" aria-busy aria-label={t("common.loading")}>
        {[0, 1, 2, 3].map((i) => (
          <section key={i} className="card glass kpi">
            <Skeleton style={{ width: "50%" }} />
            <Skeleton style={{ width: "40%", height: 28, marginTop: 8 }} />
          </section>
        ))}
      </div>
    );
  }

  const saveLabel = save.isPending ? t("dashboard.builder.saving") : dirty ? t("dashboard.builder.saving") : t("dashboard.builder.saved");

  return (
    <div className="flex flex-col gap-4">
      <div className="flex flex-wrap items-center gap-2">
        <Button size="sm" variant={editing ? "primary" : "glass"} onClick={() => setEditing((v) => !v)}>
          {editing ? t("dashboard.builder.done") : t("dashboard.builder.customize")}
        </Button>
        {editing ? (
          <>
            <Button size="sm" variant="glass" onClick={reset}>
              <RotateCcw size={14} aria-hidden />
              {t("dashboard.builder.reset")}
            </Button>
            <Button size="sm" variant="glass" onClick={exportLayout}>
              <Download size={14} aria-hidden />
              {t("dashboard.builder.export")}
            </Button>
            <label className="btn btn-glass btn-sm cursor-pointer">
              <Upload size={14} aria-hidden />
              {t("dashboard.builder.import")}
              <input
                type="file"
                accept="application/json,.json"
                className="hidden"
                onChange={(e) => {
                  const f = e.target.files?.[0];
                  if (f) importLayout(f);
                  e.target.value = "";
                }}
              />
            </label>
            <Button size="sm" variant="glass" onClick={makeDefault} disabled={save.isPending}>
              <Globe size={14} aria-hidden />
              {t("dashboard.builder.setDefault")}
            </Button>
            <span className="text-xs text-[var(--ink-500)]" aria-live="polite">
              {saveLabel}
            </span>
          </>
        ) : null}
      </div>
      {editing && mobile ? null : editing ? <Palette onAdd={addWidget} /> : null}
      {mobile ? (
        <div className="flex flex-col gap-4">
          {widgets.map((w) =>
            !editing && widgets.length > LAZY_AFTER ? (
              <LazyMount key={w.id} title={WIDGETS[w.type].title()}>
                <MobileWidget w={w} />
              </LazyMount>
            ) : (
              <MobileWidget key={w.id} w={w} />
            ),
          )}
        </div>
      ) : (
        <BuilderCanvas widgets={widgets} editing={editing} onChange={commit} onDuplicate={duplicate} onRemove={remove} onConfigure={configure} />
      )}
      {editing && mobile ? <Palette onAdd={addWidget} /> : null}
    </div>
  );
}

function WidgetSettings({ w, onConfigure, onClose }: { w: WidgetInstance; onConfigure: (id: string, config: WidgetConfig | undefined) => void; onClose: () => void }) {
  const cfg = w.config ?? {};
  const set = (patch: WidgetConfig) => onConfigure(w.id, { ...cfg, ...patch });
  const setAlert = (op: "gt" | "lt", value: string) => {
    if (value.trim() === "") {
      const { alert: _alert, ...rest } = cfg;
      void _alert;
      onConfigure(w.id, rest);
      return;
    }
    const n = Number(value);
    if (Number.isFinite(n)) onConfigure(w.id, { ...cfg, alert: { op, value: n } });
  };
  return (
    <div className="panel-soft absolute left-1 top-8 z-20 w-56 rounded-xl p-3 text-[13px] shadow-lg" role="dialog" aria-label={t("dashboard.builder.settings")}>
      {w.type === "stat" ? (
        <div className="flex flex-col gap-1.5">
          <div className="text-xs font-medium text-[var(--ink-500)]">{t("dashboard.builder.metric")}</div>
          {STAT_METRICS.map((m) => (
            <button
              key={m}
              type="button"
              className={`rounded-lg px-2 py-1 text-left ${defaultMetric(cfg) === m ? "bg-[var(--scilla-100)] font-medium" : "hover:bg-[var(--hover)]"}`}
              onClick={() => set({ metric: m as StatMetric })}
            >
              {m === "online" ? t("dashboard.online") : m === "active" ? t("dashboard.active") : m === "traffic" ? t("dashboard.trafficToday") : t("dashboard.expiring7d")}
            </button>
          ))}
          <div className="mt-1 border-t border-[var(--scilla-200)] pt-2" />
          <div className="text-xs font-medium text-[var(--ink-500)]">{t("dashboard.builder.threshold")}</div>
          <div className="flex items-center gap-1.5">
            <div className="flex overflow-hidden rounded-lg border border-[var(--scilla-200)]">
              {(["gt", "lt"] as const).map((op) => (
                <button
                  key={op}
                  type="button"
                  className={`px-2 py-1 text-sm ${cfg.alert?.op === op ? "bg-[var(--scilla-100)] font-medium" : "hover:bg-[var(--hover)]"}`}
                  onClick={() => setAlert(op, cfg.alert ? String(cfg.alert.value) : "")}
                  aria-label={op === "gt" ? t("dashboard.builder.thresholdGt") : t("dashboard.builder.thresholdLt")}
                >
                  {op === "gt" ? ">" : "<"}
                </button>
              ))}
            </div>
            <input
              className="input w-24"
              type="number"
              min={0}
              step="any"
              value={cfg.alert?.value ?? ""}
              placeholder={t("dashboard.builder.thresholdValue")}
              aria-label={t("dashboard.builder.threshold")}
              onChange={(e) => setAlert(cfg.alert?.op ?? "gt", e.target.value)}
            />
          </div>
        </div>
      ) : w.type === "traffic" || w.type === "shares" ? (
        <div className="flex flex-col gap-1.5">
          <div className="text-xs font-medium text-[var(--ink-500)]">{t("dashboard.period")}</div>
          {(["24h", "7d", "30d"] as const).map((r) => (
            <button key={r} type="button" className={`rounded-lg px-2 py-1 text-left ${cfg.range === r ? "bg-[var(--scilla-100)] font-medium" : "hover:bg-[var(--hover)]"}`} onClick={() => set({ range: r })}>
              {r === "24h" ? t("dashboard.range24h") : r === "7d" ? t("dashboard.range7d") : t("dashboard.range30d")}
            </button>
          ))}
        </div>
      ) : (
        <p className="text-xs text-[var(--ink-500)]">{t("dashboard.builder.noSettings")}</p>
      )}
      <button type="button" className="link-btn mt-2 text-[13px]" onClick={onClose}>
        {t("common.close")}
      </button>
    </div>
  );
}

function Palette({ onAdd }: { onAdd: (type: WidgetType) => void }) {
  return (
    <section className="card glass p-3" aria-label={t("dashboard.builder.palette")}>
      <div className="mb-2 text-xs font-medium uppercase tracking-wide text-[var(--ink-500)]">{t("dashboard.builder.palette")}</div>
      <div className="flex flex-wrap gap-2">
        {WIDGET_TYPES.map((type) => (
          <button key={type} type="button" className="btn btn-glass btn-sm" onClick={() => onAdd(type)}>
            <Plus size={14} aria-hidden />
            {WIDGETS[type].title()}
          </button>
        ))}
      </div>
    </section>
  );
}

function MobileWidget({ w }: { w: WidgetInstance }) {
  const def = WIDGETS[w.type];
  return <div className="min-h-[220px]">{def.component(w.config)}</div>;
}

type Gesture = { mode: "move" | "resize"; pointerId: number; startX: number; startY: number; x: number; y: number; w: number; h: number };

/** Renders the real widget only when its cell comes near the viewport; until then a titled placeholder stands in. */
function LazyMount({ title, children }: { title: string; children: React.ReactNode }) {
  const ref = useRef<HTMLDivElement>(null);
  const [near, setNear] = useState(false);
  useEffect(() => {
    if (near) return;
    const el = ref.current;
    if (!el || typeof IntersectionObserver === "undefined") {
      setNear(true);
      return;
    }
    const io = new IntersectionObserver(
      (entries) => {
        if (entries.some((e) => e.isIntersecting)) {
          setNear(true);
          io.disconnect();
        }
      },
      { rootMargin: "320px" },
    );
    io.observe(el);
    return () => io.disconnect();
  }, [near]);
  if (near) return <div className="h-full">{children}</div>;
  return (
    <div ref={ref} className="card glass flex h-full min-h-[120px] flex-col gap-3 p-4" role="status" aria-label={title}>
      <div className="text-[13px] font-medium">{title}</div>
      <Skeleton style={{ height: 28 }} />
      <Skeleton />
    </div>
  );
}

function BuilderCanvas({ widgets, editing, onChange, onDuplicate, onRemove, onConfigure }: { widgets: WidgetInstance[]; editing: boolean; onChange: (w: WidgetInstance[]) => void; onDuplicate: (w: WidgetInstance) => void; onRemove: (id: string) => void; onConfigure: (id: string, config: WidgetConfig | undefined) => void }) {
  const wrap = useRef<HTMLDivElement>(null);
  const [settingsFor, setSettingsFor] = useState<string | null>(null);
  const height = useMemo(() => bottomOf(widgets) * (ROW_H + ROW_GAP), [widgets]);
  const lazy = !editing && widgets.length > LAZY_AFTER;

  const patch = (id: string, next: Partial<WidgetInstance>) => onChange(widgets.map((w) => (w.id === id ? { ...w, ...next } : w)));

  const start = (e: React.PointerEvent, w: WidgetInstance, mode: "move" | "resize") => {
    if (!editing || e.button !== 0) return;
    e.preventDefault();
    const el = (e.currentTarget as HTMLElement).closest("[data-cell]") as HTMLElement | null;
    const rect = wrap.current?.getBoundingClientRect();
    if (!el || !rect) return;
    const g: Gesture = { mode, pointerId: e.pointerId, startX: e.clientX, startY: e.clientY, x: w.x, y: w.y, w: w.w, h: w.h };
    (e.currentTarget as HTMLElement).setPointerCapture(e.pointerId);
    const cellW = rect.width / GRID_COLS;
    const cellH = ROW_H + ROW_GAP;
    const onMove = (ev: PointerEvent) => {
      if (ev.pointerId !== g.pointerId) return;
      const dx = Math.round((ev.clientX - g.startX) / cellW);
      const dy = Math.round((ev.clientY - g.startY) / cellH);
      if (g.mode === "move") {
        const nx = clamp(g.x + dx, 0, GRID_COLS - g.w);
        const ny = clamp(g.y + dy, 0, MAX_ROWS - g.h);
        patch(w.id, { x: nx, y: ny });
      } else {
        const nw = clamp(g.w + dx, WIDGETS[w.type].minW, GRID_COLS - g.x);
        const nh = clamp(g.h + dy, WIDGETS[w.type].minH, 16);
        patch(w.id, { w: nw, h: nh });
      }
    };
    const onUp = (ev: PointerEvent) => {
      if (ev.pointerId !== g.pointerId) return;
      window.removeEventListener("pointermove", onMove);
      window.removeEventListener("pointerup", onUp);
      void cellH;
      void el;
    };
    window.addEventListener("pointermove", onMove);
    window.addEventListener("pointerup", onUp);
  };

  return (
    <div ref={wrap} className="relative" style={{ height }}>
      {widgets.map((w) => {
        const def = WIDGETS[w.type];
        return (
          <div
            key={w.id}
            data-cell
            className={`absolute ${editing ? "outline-1 outline-dashed outline-[var(--scilla-400)]" : ""}`}
            style={{
              left: `calc(${(w.x / GRID_COLS) * 100}% + ${w.x * ROW_GAP}px)`,
              top: w.y * (ROW_H + ROW_GAP),
              width: `calc(${(w.w / GRID_COLS) * 100}% - ${(ROW_GAP * (GRID_COLS - 1)) / GRID_COLS}px)`,
              height: w.h * ROW_H + (w.h - 1) * ROW_GAP,
            }}
          >
            <div className="h-full">
              {lazy ? (
                <LazyMount title={def.title()}>{def.component(w.config)}</LazyMount>
              ) : (
                def.component(w.config)
              )}
            </div>
            {editing ? (
              <>
                <div className="absolute -left-1 -top-1 z-10 flex items-center gap-1 rounded-full bg-[var(--ink-900)] px-1.5 py-0.5 text-white shadow">
                  <button
                    type="button"
                    className="cursor-grab touch-none p-0.5 active:cursor-grabbing"
                    aria-label={t("dashboard.builder.drag")}
                    onPointerDown={(e) => start(e, w, "move")}
                  >
                    <GripVertical size={14} aria-hidden />
                  </button>
                  <button type="button" className="p-0.5" aria-label={t("dashboard.builder.settings")} onClick={() => setSettingsFor(settingsFor === w.id ? null : w.id)}>
                    <SlidersHorizontal size={12} aria-hidden />
                  </button>
                  <button type="button" className="p-0.5" aria-label={t("dashboard.builder.duplicate")} onClick={() => onDuplicate(w)}>
                    <Copy size={12} aria-hidden />
                  </button>
                  <button type="button" className="p-0.5" aria-label={t("dashboard.builder.remove")} onClick={() => onRemove(w.id)}>
                    <Trash2 size={12} aria-hidden />
                  </button>
                </div>
                <button
                  type="button"
                  className="absolute -bottom-1 -right-1 z-10 h-3 w-3 cursor-nwse-resize touch-none rounded-sm border border-[var(--scilla-400)] bg-white shadow"
                  aria-label={t("dashboard.builder.resize")}
                  onPointerDown={(e) => start(e, w, "resize")}
                />
                {settingsFor === w.id ? <WidgetSettings w={w} onConfigure={onConfigure} onClose={() => setSettingsFor(null)} /> : null}
              </>
            ) : null}
          </div>
        );
      })}
    </div>
  );
}
