import * as React from "react";
import { Link } from "react-router-dom";
import { useTranslation } from "react-i18next";
import { useJobs } from "@/api/queries";
import { Badge, statusVariant } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Select } from "@/components/ui/input";
import { EmptyState, PageHeader, Spinner, Table, Td, Th } from "@/components/ui/misc";
import { formatDateTime, fromNow } from "@/lib/format";

function duration(start: number | null, end: number | null): string {
  if (!start) return "—";
  const stop = end ?? Math.floor(Date.now() / 1000);
  const s = Math.max(0, stop - start);
  if (s < 60) return `${s}s`;
  if (s < 3600) return `${Math.floor(s / 60)}m ${s % 60}s`;
  return `${Math.floor(s / 3600)}h ${Math.floor((s % 3600) / 60)}m`;
}

export function JobsPage() {
  const { t, i18n } = useTranslation();
  const [status, setStatus] = React.useState("");
  const { data, isLoading } = useJobs({ status });

  return (
    <div>
      <PageHeader title={t("job.title")} subtitle={t("job.subtitle")} />
      <div className="mb-4">
        <Select value={status} onChange={(e) => setStatus(e.target.value)} className="sm:w-44">
          <option value="">{t("common.all")}</option>
          {["queued", "running", "succeeded", "failed", "canceled", "interrupted"].map((s) => (
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
        <EmptyState description={t("job.empty")} />
      ) : (
        <Table>
          <thead>
            <tr>
              <Th>{t("job.type")}</Th>
              <Th>{t("common.status")}</Th>
              <Th>{t("job.attempts")}</Th>
              <Th>{t("job.errorCode")}</Th>
              <Th>{t("common.createdAt")}</Th>
              <Th>{t("job.duration")}</Th>
              <Th />
            </tr>
          </thead>
          <tbody>
            {data.items.map((job) => (
              <tr key={job.id} className="hover:bg-muted/30">
                <Td className="font-medium">{t(`job.types.${job.type}`)}</Td>
                <Td>
                  <Badge variant={statusVariant(job.status)}>{t(`status.${job.status}`)}</Badge>
                </Td>
                <Td className="tabular-nums">
                  {job.attempts}/{job.max_attempts}
                </Td>
                <Td className="font-mono text-xs text-destructive">{job.error_code ?? "—"}</Td>
                <Td className="text-muted-foreground" title={formatDateTime(job.created_at)}>
                  {fromNow(job.created_at, i18n.language)}
                </Td>
                <Td className="text-muted-foreground">{duration(job.started_at, job.finished_at)}</Td>
                <Td className="text-right">
                  <Button asChild variant="ghost" size="sm">
                    <Link to={`/jobs/${job.id}`}>{t("common.details")}</Link>
                  </Button>
                </Td>
              </tr>
            ))}
          </tbody>
        </Table>
      )}
    </div>
  );
}
