import { useTranslation } from "react-i18next";
import { useAudit } from "@/api/queries";
import { Badge, statusVariant } from "@/components/ui/badge";
import { EmptyState, PageHeader, Spinner, Table, Td, Th } from "@/components/ui/misc";
import { formatDateTime } from "@/lib/format";

export function AuditPage() {
  const { t } = useTranslation();
  const { data, isLoading } = useAudit();

  return (
    <div>
      <PageHeader title={t("audit.title")} subtitle={t("audit.subtitle")} />
      {isLoading ? (
        <div className="flex justify-center py-16">
          <Spinner className="h-6 w-6" />
        </div>
      ) : !data?.items.length ? (
        <EmptyState description={t("audit.empty")} />
      ) : (
        <Table>
          <thead>
            <tr>
              <Th>{t("audit.action")}</Th>
              <Th>{t("audit.resource")}</Th>
              <Th>{t("audit.result")}</Th>
              <Th>{t("audit.ip")}</Th>
              <Th>{t("common.createdAt")}</Th>
              <Th>{t("audit.requestId")}</Th>
            </tr>
          </thead>
          <tbody>
            {data.items.map((log) => (
              <tr key={log.id}>
                <Td className="font-mono text-xs">{log.action}</Td>
                <Td>
                  <div>{log.resource_type}</div>
                  {log.resource_id && <div className="font-mono text-[10px] text-muted-foreground">{log.resource_id}</div>}
                  {log.detail_json && <div className="max-w-[22rem] truncate text-xs text-muted-foreground">{log.detail_json}</div>}
                </Td>
                <Td>
                  <Badge variant={statusVariant(log.result)}>{t(`audit.${log.result}`)}</Badge>
                </Td>
                <Td className="font-mono text-xs text-muted-foreground">{log.ip ?? "—"}</Td>
                <Td className="text-muted-foreground">{formatDateTime(log.created_at)}</Td>
                <Td className="font-mono text-[10px] text-muted-foreground">{log.request_id ?? "—"}</Td>
              </tr>
            ))}
          </tbody>
        </Table>
      )}
    </div>
  );
}
