import { Link } from "@tanstack/react-router";
import { Plus } from "lucide-react";
import { useNode } from "../../api/hooks";
import { PageHeader } from "../../components/ui";
import { getLocale, t } from "../../i18n";
import { DashboardBuilder } from "./dashboard-builder";

export function Dashboard() {
  const node = useNode();
  const status = node.data ? (node.data.ok ? t("dashboard.statusOk") : t("dashboard.statusDown")) : t("dashboard.statusChecking");
  const today = new Intl.DateTimeFormat(getLocale(), { weekday: "long", day: "numeric", month: "long" }).format(new Date());
  return (
    <>
      <PageHeader
        title={t("dashboard.title")}
        sub={`${today.charAt(0).toUpperCase()}${today.slice(1)} · ${status}`}
        actions={
          <Link to="/users" search={{ state: "all", q: "", create: true }} className="btn btn-primary">
            <Plus size={18} aria-hidden />
            <span className="max-[760px]:hidden">{t("dashboard.newUser")}</span>
          </Link>
        }
      />
      <DashboardBuilder />
    </>
  );
}
