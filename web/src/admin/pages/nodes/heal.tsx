import { RotateCcw, ShieldCheck } from "lucide-react";
import { errorText } from "../../../api/client";
import { useNodeHeal, useResetNodeHeal, useSaveNodeHeal } from "../../../api/hooks";
import { Button, Pill, Skeleton } from "../../../components/ui";
import { useToast } from "../../../components/toast";
import { t } from "../../../i18n";

function phaseTone(phase: string): "ok" | "warn" | "bad" | "off" {
  if (phase === "active") return "warn";
  if (phase === "circuit_open") return "bad";
  return "off";
}

function phaseText(phase: string): string {
  if (phase === "active") return t("nodes.healPhaseActive");
  if (phase === "circuit_open") return t("nodes.healPhaseCircuit");
  return t("nodes.healPhaseWaiting");
}

function fmtTime(sec: number): string {
  return sec > 0 ? new Date(sec * 1000).toLocaleString() : "—";
}

/** The auto-heal card of the nodes page: the policy switch and every node's healing state. */
export function HealCard({ remote }: { remote: boolean }) {
  const heal = useNodeHeal();
  const save = useSaveNodeHeal();
  const reset = useResetNodeHeal();
  const toast = useToast();
  if (!remote) return null;
  const data = heal.data;
  const rows = (data?.nodes ?? []).filter((n) => n.state.phase !== "" || n.state.incident !== 0 || n.state.history.length > 0);
  return (
    <section className="card glass">
      <div className="flex flex-wrap items-start justify-between gap-3">
        <div className="min-w-0">
          <h2 className="font-display flex items-center gap-2 text-lg font-medium tracking-tight">
            <ShieldCheck size={18} aria-hidden />
            {t("nodes.healTitle")}
          </h2>
          <p className="mt-1 text-[13px] text-[var(--ink-500)]">{t("nodes.healText")}</p>
        </div>
        {data ? (
          <Button
            loading={save.isPending}
            onClick={() => save.mutate({ ...data.policy, enabled: !data.policy.enabled }, { onError: (e) => toast.error(errorText(e)) })}
          >
            {data.policy.enabled ? t("nodes.healDisable") : t("nodes.healEnable")}
          </Button>
        ) : null}
      </div>
      {!data ? (
        <Skeleton className="mt-3" style={{ height: 72 }} />
      ) : (
        <>
          {data.policy.enabled ? (
            <p className="mt-2 text-xs text-[var(--ink-500)]">
              {t("nodes.healPolicy", {
                down: Math.round(data.policy.down_for_sec / 60),
                cooldown: Math.round(data.policy.cooldown_sec / 60),
                attempts: data.policy.max_attempts,
              })}
            </p>
          ) : null}
          {rows.length === 0 ? (
            <p className="mt-3 text-xs text-[var(--ink-500)]">{t("nodes.healNoIncidents")}</p>
          ) : (
            <ul className="mt-3 flex flex-col gap-3">
              {rows.map((n) => {
                const st = n.state;
                const phase = st.phase === "" ? "active" : st.phase;
                return (
                  <li key={n.id} className="min-w-0">
                    <div className="flex flex-wrap items-center gap-2">
                      <span className="font-display text-[15px] font-medium tracking-tight">{n.name}</span>
                      <Pill tone={phaseTone(phase)}>{phaseText(phase)}</Pill>
                      {st.incident !== 0 || st.attempts > 0 ? (
                        <span className="text-xs text-[var(--ink-500)]">{t("nodes.healAttempts", { n: st.attempts })}</span>
                      ) : null}
                      {st.phase === "circuit_open" ? (
                        <Button
                          className="ml-auto"
                          loading={reset.isPending && reset.variables === n.id}
                          onClick={() =>
                            reset.mutate(n.id, {
                              onSuccess: () => toast.ok(t("nodes.healResetDone")),
                              onError: (e) => toast.error(errorText(e)),
                            })
                          }
                        >
                          <RotateCcw size={16} aria-hidden />
                          {t("nodes.healReset")}
                        </Button>
                      ) : null}
                    </div>
                    {st.history.length > 0 ? (
                      <ul className="mt-1.5 flex flex-col gap-1 text-xs text-[var(--ink-500)]">
                        {st.history.slice(0, 3).map((inc, i) => (
                          <li key={i}>
                            {inc.result === "healed" ? t("nodes.healResultHealed") : t("nodes.healResultFailed")} · {fmtTime(inc.end_at)} ·{" "}
                            {t("nodes.healAttempts", { n: inc.attempts })}
                          </li>
                        ))}
                      </ul>
                    ) : null}
                  </li>
                );
              })}
            </ul>
          )}
        </>
      )}
    </section>
  );
}
