import * as React from "react";
import { Link } from "react-router-dom";
import { useTranslation } from "react-i18next";
import { Plus } from "lucide-react";
import { useAuth } from "@/auth";
import { useCA, useCertificates } from "@/api/queries";
import type { CertificateStatus } from "@/api/types";
import { Badge, statusVariant } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Input, Select } from "@/components/ui/input";
import { EmptyState, PageHeader, Spinner, Table, Td, Th } from "@/components/ui/misc";
import { daysUntil, formatDate, fromNow } from "@/lib/format";

export function CertificatesPage() {
  const { t, i18n } = useTranslation();
  const { user } = useAuth();
  const [status, setStatus] = React.useState("");
  const [search, setSearch] = React.useState("");
  const deferred = React.useDeferredValue(search);
  const { data, isLoading } = useCertificates({ status, q: deferred });
  const { data: caData } = useCA();
  const caName = (id: string) => caData?.items.find((c) => c.id === id)?.name ?? "—";

  const canEdit = user?.role === "admin" || user?.role === "operator";

  return (
    <div>
      <PageHeader
        title={t("cert.title")}
        subtitle={t("cert.subtitle")}
        actions={
          canEdit && (
            <Button asChild>
              <Link to="/certificates/new">
                <Plus className="h-4 w-4" /> {t("cert.new")}
              </Link>
            </Button>
          )
        }
      />

      <div className="mb-4 flex flex-col gap-2 sm:flex-row">
        <Input
          placeholder={t("common.searchPlaceholder")}
          value={search}
          onChange={(e) => setSearch(e.target.value)}
          className="sm:max-w-xs"
        />
        <Select value={status} onChange={(e) => setStatus(e.target.value)} className="sm:w-44">
          <option value="">{t("common.all")}</option>
          {(["draft", "queued", "issuing", "active", "renewing", "failed", "revoked", "disabled"] as CertificateStatus[]).map((s) => (
            <option key={s} value={s}>
              {t(`status.${s}`)}
            </option>
          ))}
        </Select>
      </div>

      {isLoading ? (
        <div className="flex justify-center py-16">
          <Spinner className="h-6 w-6" />
        </div>
      ) : !data?.items.length ? (
        <EmptyState description={t("cert.empty")} />
      ) : (
        <Table>
          <thead>
            <tr>
              <Th>{t("common.name")}</Th>
              <Th>{t("cert.ca")}</Th>
              <Th>{t("common.status")}</Th>
              <Th>{t("cert.expires")}</Th>
              <Th>{t("cert.autoRenew")}</Th>
              <Th>{t("common.updatedAt")}</Th>
              <Th />
            </tr>
          </thead>
          <tbody>
            {data.items.map((c) => {
              const days = daysUntil(c.next_renew_at);
              return (
                <tr key={c.id} className="hover:bg-muted/30">
                  <Td>
                    <Link to={`/certificates/${c.id}`} className="font-medium hover:text-primary">
                      {c.name}
                    </Link>
                    <div className="text-xs text-muted-foreground">{c.primary_domain}</div>
                  </Td>
                  <Td className="text-muted-foreground">{caName(c.ca_account_id)}</Td>
                  <Td>
                    <Badge variant={statusVariant(c.status)}>{t(`status.${c.status}`)}</Badge>
                  </Td>
                  <Td>
                    {c.next_renew_at ? (
                      <div>
                        <div>{formatDate(c.next_renew_at)}</div>
                        <div className="text-xs text-muted-foreground">
                          {days !== null
                            ? i18n.language.startsWith("zh")
                              ? `${days} 天后续期`
                              : `renews in ${days}d`
                            : ""}
                        </div>
                      </div>
                    ) : (
                      <span className="text-muted-foreground">—</span>
                    )}
                  </Td>
                  <Td>
                    <Badge variant={c.auto_renew ? "success" : "muted"}>
                      {c.auto_renew ? t("common.yes") : t("common.no")}
                    </Badge>
                  </Td>
                  <Td className="text-muted-foreground" title={formatDate(c.updated_at)}>
                    {fromNow(c.updated_at, i18n.language)}
                  </Td>
                  <Td className="text-right">
                    <Button asChild variant="ghost" size="sm">
                      <Link to={`/certificates/${c.id}`}>{t("common.details")}</Link>
                    </Button>
                  </Td>
                </tr>
              );
            })}
          </tbody>
        </Table>
      )}
    </div>
  );
}
