import { Link } from "react-router-dom";
import { useTranslation } from "react-i18next";
import { useQueryClient } from "@tanstack/react-query";
import { toast } from "sonner";
import { Trash2 } from "lucide-react";
import { useAuth } from "@/auth";
import { useDeployments } from "@/api/queries";
import { errorMessage, request } from "@/api/client";
import { Badge, statusVariant } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { EmptyState, PageHeader, Table, Td, Th } from "@/components/ui/misc";
import { formatDateTime } from "@/lib/format";

export function DeploymentsPage() {
  const { t } = useTranslation();
  const { user } = useAuth();
  const qc = useQueryClient();
  const { data } = useDeployments();
  const canEdit = user?.role === "admin" || user?.role === "operator";

  return (
    <div>
      <PageHeader title={t("deploy.title")} subtitle={t("deploy.subtitle")} />
      {!data?.items.length ? (
        <EmptyState description={t("deploy.empty")} />
      ) : (
        <Table>
          <thead>
            <tr>
              <Th>{t("common.name")}</Th>
              <Th>{t("common.type")}</Th>
              <Th>{t("cert.title")}</Th>
              <Th>{t("common.status")}</Th>
              <Th>{t("deploy.lastRun")}</Th>
              <Th />
            </tr>
          </thead>
          <tbody>
            {data.items.map((d) => (
              <tr key={d.id}>
                <Td className="font-medium">{d.name}</Td>
                <Td>{d.type}</Td>
                <Td>
                  <Link to={`/certificates/${d.certificate_id}`} className="text-primary hover:underline">
                    {d.certificate_id}
                  </Link>
                </Td>
                <Td>
                  <Badge variant={statusVariant(d.last_status ?? "ok")}>{d.last_status ?? "—"}</Badge>
                </Td>
                <Td className="text-muted-foreground">{d.last_run_at ? formatDateTime(d.last_run_at) : t("common.never")}</Td>
                <Td className="text-right">
                  {canEdit && (
                    <Button
                      variant="ghost"
                      size="icon"
                      onClick={async () => {
                        if (!window.confirm(`${t("common.delete")} ${d.name}?`)) return;
                        try {
                          await request(`/deployments/${d.id}`, { method: "DELETE" });
                          toast.success(t("deploy.deleted"));
                          void qc.invalidateQueries();
                        } catch (err) {
                          toast.error(errorMessage(err));
                        }
                      }}
                    >
                      <Trash2 className="h-4 w-4 text-destructive" />
                    </Button>
                  )}
                </Td>
              </tr>
            ))}
          </tbody>
        </Table>
      )}
    </div>
  );
}
